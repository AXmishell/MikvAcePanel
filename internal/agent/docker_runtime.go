package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

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
	// versionCheckedAt is the last time detectVersion ran. Guarded by mu.
	versionCheckedAt time.Time
}

func (d *dockerRuntime) Name() string       { return d.protocol }
func (d *dockerRuntime) ConfigPath() string { return d.configPath }

func (d *dockerRuntime) Version() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.version
}

func (d *dockerRuntime) RefreshVersion() {
	d.mu.Lock()
	if !d.started {
		d.mu.Unlock()
		return
	}
	if !d.versionCheckedAt.IsZero() && time.Since(d.versionCheckedAt) < versionRefreshInterval {
		d.mu.Unlock()
		return
	}
	d.versionCheckedAt = time.Now()
	d.mu.Unlock()
	d.detectVersion()
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
	d.mu.Lock()
	d.versionCheckedAt = time.Now()
	d.mu.Unlock()
	bin := "hysteria"
	if d.protocol == "xray" {
		bin = "xray"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "exec", d.container, bin, "version").CombinedOutput()
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
