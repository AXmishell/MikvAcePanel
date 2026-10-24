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

// Config is node agent configuration, loaded from node.yaml when present and
// overlaid with environment variables.
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
	if st, err := os.Stat("/config"); err == nil && st.IsDir() && !isDir("/config/node.yaml") {
		return "/config/node.yaml"
	}
	if st, err := os.Stat("/data"); err == nil && st.IsDir() && !isDir("/data/node.yaml") {
		return "/data/node.yaml"
	}
	return "node.yaml"
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// Environment variables understood by the node agent. They take precedence
// over node.yaml so a container can be configured without any mounted file.
const (
	envPanelURL          = "PANEL_URL"
	envNodeToken         = "NODE_TOKEN"
	envNodeTokenFile     = "NODE_TOKEN_FILE"
	envRuntime           = "HY2_RUNTIME"
	envPanelInsecure     = "PANEL_INSECURE"
	envDataDir           = "DATA_DIR"
	envHostDataDir       = "HOST_DATA_DIR"
	envLogLevel          = "LOG_LEVEL"
	envHeartbeat         = "HEARTBEAT"
	envStatsInterval     = "STATS_INTERVAL"
	envOfflineAuth       = "OFFLINE_AUTH"
	envOfflineAuthListen = "OFFLINE_AUTH_LISTEN"
	envHysteriaBin       = "HYSTERIA_BIN"
	envHysteriaImage     = "HYSTERIA_IMAGE"
	envXrayBin           = "XRAY_BIN"
	envXrayImage         = "XRAY_IMAGE"
	envContainerName     = "CONTAINER_NAME"
)

func envString(name string, dst *string) {
	if v, ok := os.LookupEnv(name); ok {
		*dst = strings.TrimSpace(v)
	}
}

// envStringAny accepts a primary name plus legacy aliases.
func envStringAny(dst *string, names ...string) {
	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			*dst = strings.TrimSpace(v)
			return
		}
	}
}

func envBool(name string, dst *bool) error {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		*dst = true
	case "0", "false", "no", "off":
		*dst = false
	default:
		return fmt.Errorf("%s: invalid boolean %q", name, raw)
	}
	return nil
}

// applyEnv overlays environment variables on top of the file configuration.
func applyEnv(fc *fileConfig) error {
	envString(envPanelURL, &fc.Panel.URL)
	envString(envNodeTokenFile, &fc.Panel.TokenFile)
	envString(envRuntime, &fc.Runtime.Type)
	envString(envDataDir, &fc.Agent.DataDir)
	envString(envHostDataDir, &fc.Agent.HostDataDir)
	envString(envLogLevel, &fc.Agent.LogLevel)
	envString(envHeartbeat, &fc.Agent.Heartbeat)
	envString(envStatsInterval, &fc.Agent.StatsInterval)
	envString(envHysteriaBin, &fc.Runtime.HysteriaBin)
	envStringAny(&fc.Runtime.HysteriaImage, envHysteriaImage, "HY2_IMAGE")
	envString(envXrayBin, &fc.Runtime.XrayBin)
	envString(envXrayImage, &fc.Runtime.XrayImage)
	envStringAny(&fc.Runtime.ContainerName, envContainerName, "HY2_CONTAINER")
	envStringAny(&fc.OfflineAuth.Listen, envOfflineAuthListen, "AUTH_LISTEN")
	if err := envBool(envPanelInsecure, &fc.Panel.Insecure); err != nil {
		return err
	}
	return envBool(envOfflineAuth, &fc.OfflineAuth.Enabled)
}

// ParseConfig loads node.yaml when present and then applies environment
// variables. A container configured purely through PANEL_URL/NODE_TOKEN works
// without any configuration file; an explicit --config that is missing is
// still an error.
func ParseConfig(args []string, version string) (Config, error) {
	path := DefaultNodeConfigPath()
	fs := flag.NewFlagSet("mikvace-node", flag.ContinueOnError)
	fs.StringVar(&path, "config", path, "path to node.yaml (optional when using environment variables)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	explicitConfig := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			explicitConfig = true
		}
	})
	if isDir(path) {
		if explicitConfig {
			return Config{}, fmt.Errorf("config path %q is a directory, not a file", path)
		}
		// A stale directory at the default location must not prevent
		// environment-only configuration.
		path = ""
	}
	fc := defaultFileConfig()
	loaded := path != "" && configfile.Exists(path)
	if explicitConfig && !loaded {
		return Config{}, fmt.Errorf("config file %q not found", path)
	}
	if loaded {
		if err := configfile.Load(path, &fc); err != nil {
			return Config{}, err
		}
	} else {
		path = ""
	}
	if err := applyEnv(&fc); err != nil {
		return Config{}, err
	}
	token := strings.TrimSpace(os.Getenv(envNodeToken))
	tokenFromEnv := token != ""
	if !tokenFromEnv {
		var err error
		token, err = configfile.Secret(fc.Panel.TokenFile)
		if err != nil {
			return Config{}, fmt.Errorf("panel.token_file: %w", err)
		}
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
		return c, fmt.Errorf("panel.url or %s is required", envPanelURL)
	}
	u, err := url.Parse(c.PanelURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return c, fmt.Errorf("panel.url must start with http:// or https://")
	}
	if !tokenFromEnv && c.TokenFile == "" {
		return c, fmt.Errorf("panel.token_file or %s is required", envNodeToken)
	}
	if c.Token == "" {
		if tokenFromEnv {
			return c, fmt.Errorf("%s is empty", envNodeToken)
		}
		return c, fmt.Errorf("panel.token_file %q is empty", c.TokenFile)
	}
	if c.DataDir == "" || c.DataDir == "." {
		c.DataDir = "/data"
	}
	if c.HostDataDir == "" || c.HostDataDir == "." {
		c.HostDataDir = "/var/lib/mikvace"
	}
	if c.Runtime != "process" && c.Runtime != "docker" {
		return c, fmt.Errorf("runtime.type or %s must be process or docker", envRuntime)
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
