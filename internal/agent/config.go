// Package agent implements the MikvAce node agent. It keeps a websocket
// control channel to the panel, applies pushed Hysteria 2 configurations and
// ships logs, traffic counters and host metrics back to the panel.
package agent

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is node agent configuration.
type Config struct {
	PanelURL      string
	Token         string
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
}

// ParseConfig reads environment variables and optional command line flags.
func ParseConfig(args []string, version string) (Config, error) {
	c := Config{
		PanelURL:      envOr("PANEL_URL", ""),
		Token:         envOr("NODE_TOKEN", ""),
		DataDir:       envOr("DATA_DIR", "/data"),
		HostDataDir:   envOr("HOST_DATA_DIR", "/var/lib/mikvace"),
		Runtime:       envOr("HY2_RUNTIME", "process"),
		HysteriaBin:   envOr("HYSTERIA_BIN", "hysteria"),
		HysteriaImage: envOr("HY2_IMAGE", "tobyxdd/hysteria:latest"),
		XrayBin:       envOr("XRAY_BIN", "xray"),
		XrayImage:     envOr("XRAY_IMAGE", "ghcr.io/xtls/xray-core:latest"),
		ContainerName: envOr("HY2_CONTAINER", "mikvace-hysteria"),
		PanelInsecure: envBool("PANEL_INSECURE", false),
		LogLevel:      envOr("LOG_LEVEL", "info"),
		Heartbeat:     10 * time.Second,
		StatsInterval: 15 * time.Second,
		Version:       version,
	}
	fs := flag.NewFlagSet("mikvace-node", flag.ContinueOnError)
	fs.StringVar(&c.PanelURL, "panel", c.PanelURL, "panel base URL (e.g. https://panel.example.com)")
	fs.StringVar(&c.Token, "token", c.Token, "node agent token")
	fs.StringVar(&c.DataDir, "data-dir", c.DataDir, "persistent data directory")
	fs.StringVar(&c.HostDataDir, "host-data-dir", c.HostDataDir, "host path of the data directory (docker runtime)")
	fs.StringVar(&c.Runtime, "runtime", c.Runtime, "hysteria runtime: process or docker")
	fs.StringVar(&c.HysteriaBin, "hysteria-bin", c.HysteriaBin, "path to the hysteria binary")
	fs.StringVar(&c.HysteriaImage, "hysteria-image", c.HysteriaImage, "hysteria docker image")
	fs.StringVar(&c.XrayBin, "xray-bin", c.XrayBin, "path to the Xray binary")
	fs.StringVar(&c.XrayImage, "xray-image", c.XrayImage, "Xray docker image")
	fs.StringVar(&c.ContainerName, "container", c.ContainerName, "hysteria docker container name")
	fs.BoolVar(&c.PanelInsecure, "insecure", c.PanelInsecure, "skip panel TLS certificate verification")
	fs.StringVar(&c.LogLevel, "log-level", c.LogLevel, "log level")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	c.PanelURL = strings.TrimRight(strings.TrimSpace(c.PanelURL), "/")
	c.Token = strings.TrimSpace(c.Token)
	c.DataDir = filepath.Clean(c.DataDir)
	c.HostDataDir = filepath.Clean(c.HostDataDir)
	if c.PanelURL == "" {
		return c, fmt.Errorf("--panel or PANEL_URL is required")
	}
	if c.Token == "" {
		return c, fmt.Errorf("--token or NODE_TOKEN is required")
	}
	if c.Runtime != "process" && c.Runtime != "docker" {
		return c, fmt.Errorf("--runtime must be process or docker")
	}
	if c.Heartbeat < 3*time.Second {
		c.Heartbeat = 3 * time.Second
	}
	if c.StatsInterval < 5*time.Second {
		c.StatsInterval = 5 * time.Second
	}
	return c, nil
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
