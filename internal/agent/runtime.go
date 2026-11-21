package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// DesiredRunning reports whether the core should be running. A manual stop
	// clears it so automatic recovery does not fight the operator.
	DesiredRunning() bool
	// SetAutoRestart controls the runtime's own crash supervision. The panel
	// combines its global switch with the node-level setting before pushing it.
	SetAutoRestart(bool)
	Name() string
	Version() string
	// RefreshVersion re-reads the core version from the installed binary. It is
	// throttled so heartbeats can call it without spawning a process every time.
	RefreshVersion()
	ConfigPath() string
	Close()
}

// versionRefreshInterval bounds how often a core binary is queried for its
// version. Heartbeats are much more frequent (10s by default), while a binary
// upgrade is rare; 30s keeps the panel reasonably fresh without extra load.
const versionRefreshInterval = 30 * time.Second

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
			return nil, fmt.Errorf("agent.host_data_dir must be an absolute host path for the docker runtime")
		}
		if _, err := os.Stat(cfg.HostDataDir); err != nil {
			return nil, fmt.Errorf("agent.host_data_dir %q is not accessible: %w", cfg.HostDataDir, err)
		}
		return &dockerRuntime{
			cfg: cfg, sink: sink, protocol: protocol, image: image, command: command,
			container:   containerName(cfg, protocol),
			configPath:  filepath.Join(cfg.DataDir, "config", configName),
			autoRestart: true,
		}, nil
	default:
		if _, err := exec.LookPath(bin); err != nil {
			return nil, fmt.Errorf("%s binary %q not found", protocol, bin)
		}
		return &processRuntime{
			cfg: cfg, sink: sink, protocol: protocol, bin: bin,
			args:        append(args, "-c", filepath.Join(cfg.DataDir, "config", configName)),
			configPath:  filepath.Join(cfg.DataDir, "config", configName),
			autoRestart: true,
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
