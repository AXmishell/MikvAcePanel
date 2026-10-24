// Package agent implements the MikvAce node agent. It keeps a websocket
// control channel to the panel, applies pushed Hysteria 2 configurations and
// ships logs, traffic counters and host metrics back to the panel.
package agent

import (
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/configfile"
)

// Config is node agent configuration, loaded from node.yaml.
type Config struct {
	ConfigPath    string
	PanelURL      string
	Token         string
	TokenFile     string
	DataDir       string
	HostDataDir   string
	Runtime       string // process | docker
	HysteriaBin   string
	HysteriaImage string
	XrayBin       string
	XrayImage     string
	ContainerName string
	PanelInsecure bool
	LogLevel      string
	Heartbeat     time.Duration
	StatsInterval time.Duration
	Version       string

	// AuthListen is the loopback address of the node-local Hysteria auth
	// gateway. The agent rewrites auth.http.url to this address so it can fall
	// back to a cached user snapshot when the panel is unreachable.
	AuthListen string
	// OfflineAuth enables the local auth gateway and snapshot cache.
	OfflineAuth bool
}

// fileConfig mirrors node.yaml.
type fileConfig struct {
	Version int `yaml:"version"`
	Panel   struct {
		URL       string `yaml:"url"`
		TokenFile string `yaml:"token_file"`
		Insecure  bool   `yaml:"insecure"`
	} `yaml:"panel"`
	Agent struct {
		DataDir       string `yaml:"data_dir"`
		HostDataDir   string `yaml:"host_data_dir"`
		LogLevel      string `yaml:"log_level"`
		Heartbeat     string `yaml:"heartbeat"`
		StatsInterval string `yaml:"stats_interval"`
	} `yaml:"agent"`
	Runtime struct {
		Type          string `yaml:"type"`
		HysteriaBin   string `yaml:"hysteria_bin"`
		HysteriaImage string `yaml:"hysteria_image"`
		XrayBin       string `yaml:"xray_bin"`
		XrayImage     string `yaml:"xray_image"`
		ContainerName string `yaml:"container_name"`
	} `yaml:"runtime"`
	OfflineAuth struct {
		Enabled bool   `yaml:"enabled"`
		Listen  string `yaml:"listen"`
	} `yaml:"offline_auth"`
}

func defaultFileConfig() fileConfig {
	f := fileConfig{Version: 1}
	f.Agent.DataDir = "/data"
	f.Agent.HostDataDir = "/var/lib/mikvace"
	f.Agent.LogLevel = "info"
	f.Agent.Heartbeat = "10s"
	f.Agent.StatsInterval = "15s"
	f.Runtime.Type = "process"
	f.Runtime.HysteriaBin = "hysteria"
	f.Runtime.HysteriaImage = "tobyxdd/hysteria:latest"
	f.Runtime.XrayBin = "xray"
	f.Runtime.XrayImage = "ghcr.io/xtls/xray-core:latest"
	f.Runtime.ContainerName = "mikvace-hysteria"
	f.OfflineAuth.Enabled = true
	f.OfflineAuth.Listen = "127.0.0.1:9998"
	return f
}

// DefaultNodeConfigPath returns the first conventional node.yaml location.
func DefaultNodeConfigPath() string {
	for _, p := range []string{"node.yaml", "config/node.yaml", "/config/node.yaml", "/data/node.yaml"} {
		if configfile.Exists(p) {
			return p
		}
	}
	if st, err := os.Stat("/config"); err == nil && st.IsDir() {
		return "/config/node.yaml"
	}
	return "node.yaml"
}

// ParseConfig loads node.yaml. Only the config path comes from a flag.
func ParseConfig(args []string, version string) (Config, error) {
	path := DefaultNodeConfigPath()
	fs := flag.NewFlagSet("mikvace-node", flag.ContinueOnError)
	fs.StringVar(&path, "config", path, "path to node.yaml")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if !configfile.Exists(path) {
		return Config{}, fmt.Errorf("config file %q not found", path)
	}
	fc := defaultFileConfig()
	if err := configfile.Load(path, &fc); err != nil {
		return Config{}, err
	}
	token, err := configfile.Secret(fc.Panel.TokenFile)
	if err != nil {
		return Config{}, fmt.Errorf("panel.token_file: %w", err)
	}
	heartbeat, err := parseDuration(fc.Agent.Heartbeat, 10*time.Second, "agent.heartbeat")
	if err != nil {
		return Config{}, err
	}
	statsInterval, err := parseDuration(fc.Agent.StatsInterval, 15*time.Second, "agent.stats_interval")
	if err != nil {
		return Config{}, err
	}
	c := Config{
		ConfigPath:    path,
		PanelURL:      strings.TrimRight(strings.TrimSpace(fc.Panel.URL), "/"),
		Token:         token,
		TokenFile:     strings.TrimSpace(fc.Panel.TokenFile),
		DataDir:       filepath.Clean(strings.TrimSpace(fc.Agent.DataDir)),
		HostDataDir:   filepath.Clean(strings.TrimSpace(fc.Agent.HostDataDir)),
		Runtime:       strings.ToLower(strings.TrimSpace(fc.Runtime.Type)),
		HysteriaBin:   strings.TrimSpace(fc.Runtime.HysteriaBin),
		HysteriaImage: strings.TrimSpace(fc.Runtime.HysteriaImage),
		XrayBin:       strings.TrimSpace(fc.Runtime.XrayBin),
		XrayImage:     strings.TrimSpace(fc.Runtime.XrayImage),
		ContainerName: strings.TrimSpace(fc.Runtime.ContainerName),
		PanelInsecure: fc.Panel.Insecure,
		LogLevel:      strings.TrimSpace(fc.Agent.LogLevel),
		Heartbeat:     heartbeat,
		StatsInterval: statsInterval,
		Version:       version,
		AuthListen:    strings.TrimSpace(fc.OfflineAuth.Listen),
		OfflineAuth:   fc.OfflineAuth.Enabled,
	}
	if c.PanelURL == "" {
		return c, fmt.Errorf("panel.url is required")
	}
	u, err := url.Parse(c.PanelURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return c, fmt.Errorf("panel.url must start with http:// or https://")
	}
	if c.TokenFile == "" {
		return c, fmt.Errorf("panel.token_file is required")
	}
	if c.Token == "" {
		return c, fmt.Errorf("panel.token_file %q is empty", c.TokenFile)
	}
	if c.DataDir == "" || c.DataDir == "." {
		c.DataDir = "/data"
	}
	if c.HostDataDir == "" || c.HostDataDir == "." {
		c.HostDataDir = "/var/lib/mikvace"
	}
	if c.Runtime != "process" && c.Runtime != "docker" {
		return c, fmt.Errorf("runtime.type must be process or docker")
	}
	if c.HysteriaBin == "" {
		c.HysteriaBin = "hysteria"
	}
	if c.XrayBin == "" {
		c.XrayBin = "xray"
	}
	if c.HysteriaImage == "" {
		c.HysteriaImage = "tobyxdd/hysteria:latest"
	}
	if c.XrayImage == "" {
		c.XrayImage = "ghcr.io/xtls/xray-core:latest"
	}
	if c.ContainerName == "" {
		c.ContainerName = "mikvace-hysteria"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.Heartbeat < 3*time.Second {
		c.Heartbeat = 3 * time.Second
	}
	if c.StatsInterval < 5*time.Second {
		c.StatsInterval = 5 * time.Second
	}
	if c.OfflineAuth {
		if c.AuthListen == "" {
			c.AuthListen = "127.0.0.1:9998"
		}
		host, _, err := net.SplitHostPort(c.AuthListen)
		if err != nil {
			return c, fmt.Errorf("offline_auth.listen must be host:port: %w", err)
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return c, fmt.Errorf("offline_auth.listen must bind to loopback (got %q); set offline_auth.enabled=false to disable", c.AuthListen)
		}
	}
	return c, nil
}

func parseDuration(raw string, def time.Duration, field string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s: invalid duration %q", field, raw)
	}
	return d, nil
}
