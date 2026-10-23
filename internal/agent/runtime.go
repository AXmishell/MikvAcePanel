package agent

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// LogLine is one line emitted by the Hysteria runtime.
type LogLine struct {
	TS      time.Time
	Level   string
	Message string
	Raw     string
}

// Runtime abstracts running Hysteria either as a local process or a container.
type Runtime interface {
	Start() error
	Stop() error
	Restart() error
	Running() bool
	Name() string
	Version() string
	ConfigPath() string
	Close()
}

func emit(sink func(LogLine), level, msg string) {
	if sink == nil {
		return
	}
	sink(LogLine{TS: time.Now().UTC(), Level: level, Message: msg, Raw: fmt.Sprintf("%s\t%s\tagent\t%s",
		time.Now().UTC().Format("2006-01-02T15:04:05.000Z0700"), level, msg)})
}

// NewRuntime builds the configured runtime implementation for a protocol.
func NewRuntime(cfg Config, protocol string, sink func(LogLine)) (Runtime, error) {
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "config"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "certs"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "log"), 0o755); err != nil {
		return nil, err
	}
	if protocol == "" {
		protocol = "hysteria"
	}
	bin, args, configName := cfg.HysteriaBin, []string{"server"}, "config.yaml"
	image, command := cfg.HysteriaImage, []string{"server"}
	if protocol == "xray" {
		bin, args, configName = cfg.XrayBin, []string{"run"}, "config.json"
		image, command = cfg.XrayImage, []string{"run"}
	}
	switch cfg.Runtime {
	case "docker":
		if _, err := exec.LookPath("docker"); err != nil {
			return nil, fmt.Errorf("docker runtime selected but docker CLI is not installed")
		}
		if !filepath.IsAbs(cfg.HostDataDir) {
			return nil, fmt.Errorf("HOST_DATA_DIR must be an absolute host path for the docker runtime")
		}
		if _, err := os.Stat(cfg.HostDataDir); err != nil {
			return nil, fmt.Errorf("HOST_DATA_DIR %q is not accessible: %w", cfg.HostDataDir, err)
		}
		return &dockerRuntime{
			cfg: cfg, sink: sink, protocol: protocol, image: image, command: command,
			container:  containerName(cfg, protocol),
			configPath: filepath.Join(cfg.DataDir, "config", configName),
		}, nil
	default:
		if _, err := exec.LookPath(bin); err != nil {
			return nil, fmt.Errorf("%s binary %q not found", protocol, bin)
		}
		return &processRuntime{
			cfg: cfg, sink: sink, protocol: protocol, bin: bin,
			args:       append(args, "-c", filepath.Join(cfg.DataDir, "config", configName)),
			configPath: filepath.Join(cfg.DataDir, "config", configName),
		}, nil
	}
}

// containerName returns the sibling container name for a core. A hybrid node
// runs the two cores in separate containers, so they cannot share a name.
func containerName(cfg Config, protocol string) string {
	name := strings.TrimSpace(cfg.ContainerName)
	if name == "" {
		name = "mikvace-hysteria"
	}
	if protocol == "xray" {
		return name + "-xray"
	}
	return name
}

// ---- process runtime ----------------------------------------------------

type processRuntime struct {
	cfg        Config
	sink       func(LogLine)
	protocol   string
	bin        string
	args       []string
	configPath string

	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
	desired bool
	closed  bool
	version string
}

func (p *processRuntime) Name() string       { return p.protocol }
func (p *processRuntime) ConfigPath() string { return p.configPath }
func (p *processRuntime) Version() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

func (p *processRuntime) Start() error {
	p.mu.Lock()
	p.desired = true
	if p.running || p.closed {
		p.mu.Unlock()
		return nil
	}
	cmd := exec.Command(p.bin, p.args...)
	cmd.Dir = filepath.Dir(p.configPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		p.mu.Unlock()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		p.mu.Unlock()
		return err
	}
	if err := cmd.Start(); err != nil {
		p.mu.Unlock()
		return err
	}
	p.cmd = cmd
	p.running = true
	p.mu.Unlock()

	go p.detectVersion()
	emit(p.sink, "INFO", "starting "+p.protocol+" process (pid "+strconv.Itoa(cmd.Process.Pid)+")")
	go p.scan(stdout)
	go p.scan(stderr)
	go p.wait(cmd)
	return nil
}

func (p *processRuntime) scan(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if p.sink != nil {
			p.sink(LogLine{TS: time.Now().UTC(), Level: "INFO", Message: line, Raw: line})
		}
	}
}

func (p *processRuntime) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	p.mu.Lock()
	p.running = false
	p.cmd = nil
	desired := p.desired && !p.closed
	p.mu.Unlock()
	if err != nil {
		emit(p.sink, "ERROR", p.protocol+" process exited: "+err.Error())
	} else {
		emit(p.sink, "WARN", p.protocol+" process exited")
	}
	if desired {
		time.AfterFunc(3*time.Second, func() {
			p.mu.Lock()
			retry := p.desired && !p.closed && !p.running
			p.mu.Unlock()
			if retry {
				if err := p.Start(); err != nil {
					emit(p.sink, "ERROR", "restart hysteria failed: "+err.Error())
				}
			}
		})
	}
}

func (p *processRuntime) Stop() error {
	p.mu.Lock()
	p.desired = false
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	emit(p.sink, "INFO", "stopping "+p.protocol+" process")
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		running := p.running
		p.mu.Unlock()
		if !running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return nil
}

func (p *processRuntime) Restart() error {
	p.mu.Lock()
	p.desired = false
	p.mu.Unlock()
	if err := p.Stop(); err != nil {
		return err
	}
	return p.Start()
}

func (p *processRuntime) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *processRuntime) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	_ = p.Stop()
}

func (p *processRuntime) detectVersion() {
	out, err := exec.Command(p.bin, "version").CombinedOutput()
	if err != nil {
		return
	}
	p.mu.Lock()
	if p.protocol == "xray" {
		p.version = shortXrayVersion(out)
	} else {
		p.version = parseHysteriaVersion(out)
	}
	p.mu.Unlock()
}

func firstLine(out []byte) string {
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line
}

func shortXrayVersion(out []byte) string {
	fields := strings.Fields(firstLine(out))
	if len(fields) >= 2 {
		return fields[0] + " " + fields[1]
	}
	return firstLine(out)
}

func parseHysteriaVersion(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "v2.") {
			return line
		}
	}
	return "unknown"
}

// ---- docker runtime -----------------------------------------------------

type dockerRuntime struct {
	cfg        Config
	sink       func(LogLine)
	protocol   string
	image      string
	command    []string
	container  string
	configPath string

	mu      sync.Mutex
	logCmd  *exec.Cmd
	logStop chan struct{}
	version string
	started bool
}

func (d *dockerRuntime) Name() string       { return d.protocol }
func (d *dockerRuntime) ConfigPath() string { return d.configPath }

func (d *dockerRuntime) Version() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.version
}

func (d *dockerRuntime) Start() error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()
	// Remove a stale container with our name.
	_ = exec.Command("docker", "rm", "-f", d.container).Run()
	containerConfig := "/data/config/config.yaml"
	if d.protocol == "xray" {
		containerConfig = "/data/config/config.json"
	}
	args := []string{
		"run", "-d", "--name", d.container,
		"--restart", "unless-stopped", "--network", "host", "--cap-add", "NET_ADMIN",
		"-v", d.cfg.HostDataDir + ":/data",
		d.image,
	}
	args = append(args, d.command...)
	args = append(args, "-c", containerConfig)
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker run: %s: %w", strings.TrimSpace(string(out)), err)
	}
	d.mu.Lock()
	d.started = true
	d.mu.Unlock()
	emit(d.sink, "INFO", d.protocol+" docker container started")
	d.startLogs()
	d.detectVersion()
	return nil
}

func (d *dockerRuntime) startLogs() {
	d.mu.Lock()
	if d.logCmd != nil {
		d.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "docker", "logs", "-f", "--tail", "0", d.container)
	stop := make(chan struct{})
	d.logCmd = cmd
	d.logStop = stop
	d.mu.Unlock()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return
	}
	scan := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			if d.sink != nil {
				d.sink(LogLine{TS: time.Now().UTC(), Level: "INFO", Message: line, Raw: line})
			}
		}
	}
	go scan(stdout)
	go scan(stderr)
	go func() {
		_ = cmd.Wait()
		d.mu.Lock()
		d.logCmd = nil
		d.mu.Unlock()
	}()
	_ = stop
	_ = cancel
}

func (d *dockerRuntime) Stop() error {
	d.mu.Lock()
	d.started = false
	logCmd := d.logCmd
	d.mu.Unlock()
	if logCmd != nil && logCmd.Process != nil {
		_ = logCmd.Process.Kill()
	}
	emit(d.sink, "INFO", "stopping "+d.protocol+" docker container")
	out, err := exec.Command("docker", "stop", "-t", "10", d.container).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such container") {
		return fmt.Errorf("docker stop: %s: %w", strings.TrimSpace(string(out)), err)
	}
	_ = exec.Command("docker", "rm", "-f", d.container).Run()
	return nil
}

func (d *dockerRuntime) Restart() error {
	if err := d.Stop(); err != nil {
		return err
	}
	return d.Start()
}

func (d *dockerRuntime) Running() bool {
	d.mu.Lock()
	started := d.started
	d.mu.Unlock()
	if !started {
		return false
	}
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", d.container).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func (d *dockerRuntime) Close() { _ = d.Stop() }

func (d *dockerRuntime) detectVersion() {
	bin := "hysteria"
	if d.protocol == "xray" {
		bin = "xray"
	}
	out, err := exec.Command("docker", "exec", d.container, bin, "version").CombinedOutput()
	if err != nil {
		d.mu.Lock()
		d.version = "docker"
		d.mu.Unlock()
		return
	}
	d.mu.Lock()
	if d.protocol == "xray" {
		d.version = shortXrayVersion(out)
	} else {
		d.version = parseHysteriaVersion(out)
	}
	d.mu.Unlock()
}

// ---- certificates and helper setup -------------------------------------

// EnsureSelfSignedCert creates (or refreshes) the self-signed certificate used
// when a node has no ACME domain.
func EnsureSelfSignedCert(dir, host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "localhost"
	}
	certPath := filepath.Join(dir, "certs", "cert.pem")
	keyPath := filepath.Join(dir, "certs", "key.pem")
	if ok, _ := certificateValid(certPath, host); ok {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"MikvAce Panel"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip, net.ParseIP("127.0.0.1")}
	} else {
		tmpl.DNSNames = []string{host}
		if host != "localhost" {
			tmpl.DNSNames = append(tmpl.DNSNames, "localhost")
		}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certOut := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDer, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyOut := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer})
	if err := os.WriteFile(certPath, certOut, 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, keyOut, 0o600)
}

func certificateValid(path, host string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return false, fmt.Errorf("invalid certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err
	}
	if time.Now().After(cert.NotAfter.Add(-7 * 24 * time.Hour)) {
		return false, fmt.Errorf("certificate expiring")
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, got := range cert.IPAddresses {
			if got.Equal(ip) {
				return true, nil
			}
		}
		return false, nil
	}
	for _, name := range cert.DNSNames {
		if strings.EqualFold(name, host) {
			return true, nil
		}
	}
	return false, nil
}

// PrepareConfig rewrites the canonical /data paths when Hysteria runs as a
// local process instead of a sibling container.
func PrepareConfig(cfg Config, yaml string) string {
	if cfg.Runtime == "docker" {
		return yaml
	}
	dataDir := filepath.Clean(cfg.DataDir)
	if dataDir == "" || dataDir == "/data" {
		return yaml
	}
	return strings.ReplaceAll(yaml, "/data/", strings.TrimRight(dataDir, "/")+"/")
}

// RemovePortHopping deletes previously installed redirect rules.
func RemovePortHopping(port int, ranges string) {
	if strings.TrimSpace(ranges) == "" || port <= 0 {
		return
	}
	for _, part := range strings.Split(ranges, ",") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "-") {
			continue
		}
		iptRange := strings.ReplaceAll(part, "-", ":")
		_ = exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-p", "udp", "--dport", iptRange,
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(port)).Run()
	}
}

// SetupPortHopping installs the UDP redirect rules needed by Hysteria port
// hopping. Failures are reported but do not abort configuration application.
func SetupPortHopping(port int, ranges string) error {
	ranges = strings.TrimSpace(ranges)
	if ranges == "" || port <= 0 {
		return nil
	}
	if _, err := exec.LookPath("iptables"); err != nil {
		return fmt.Errorf("iptables not available: %w", err)
	}
	var errs []string
	for _, part := range strings.Split(ranges, ",") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "-") {
			continue
		}
		iptRange := strings.ReplaceAll(part, "-", ":")
		check := exec.Command("iptables", "-t", "nat", "-C", "PREROUTING", "-p", "udp", "--dport", iptRange,
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(port))
		if err := check.Run(); err == nil {
			continue
		}
		add := exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "udp", "--dport", iptRange,
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(port))
		if out, err := add.CombinedOutput(); err != nil {
			errs = append(errs, strings.TrimSpace(string(out)))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("port hopping rules: %s", strings.Join(errs, "; "))
	}
	return nil
}
