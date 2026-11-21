package agent

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type processRuntime struct {
	cfg        Config
	sink       func(LogLine)
	protocol   string
	bin        string
	args       []string
	configPath string

	mu          sync.Mutex
	cmd         *exec.Cmd
	running     bool
	desired     bool
	closed      bool
	autoRestart bool
	version     string
	// versionCheckedAt is the last time detectVersion ran. Guarded by mu.
	versionCheckedAt time.Time
}

func (p *processRuntime) Name() string       { return p.protocol }
func (p *processRuntime) ConfigPath() string { return p.configPath }
func (p *processRuntime) Version() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

func (p *processRuntime) RefreshVersion() {
	p.mu.Lock()
	if !p.versionCheckedAt.IsZero() && time.Since(p.versionCheckedAt) < versionRefreshInterval {
		p.mu.Unlock()
		return
	}
	// Reserve the slot before running the binary so concurrent callers do not
	// launch duplicate version probes.
	p.versionCheckedAt = time.Now()
	p.mu.Unlock()
	p.detectVersion()
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
	desired := p.desired && !p.closed && p.autoRestart
	p.mu.Unlock()
	if err != nil {
		emit(p.sink, "ERROR", p.protocol+" process exited: "+err.Error())
	} else {
		emit(p.sink, "WARN", p.protocol+" process exited")
	}
	if desired {
		time.AfterFunc(3*time.Second, func() {
			p.mu.Lock()
			retry := p.desired && p.autoRestart && !p.closed && !p.running
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

func (p *processRuntime) DesiredRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.desired && !p.closed
}

func (p *processRuntime) SetAutoRestart(enabled bool) {
	p.mu.Lock()
	p.autoRestart = enabled
	p.mu.Unlock()
}

func (p *processRuntime) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	_ = p.Stop()
}

func (p *processRuntime) detectVersion() {
	p.mu.Lock()
	p.versionCheckedAt = time.Now()
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p.bin, "version").CombinedOutput()
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
