package server

import (
	"context"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/axmishell/mikvacepanel/internal/model"
)

type generatedNodeConfig struct {
	Version int `yaml:"version"`
	Panel   struct {
		URL       string `yaml:"url"`
		TokenFile string `yaml:"token_file"`
		Insecure  bool   `yaml:"insecure"`
	} `yaml:"panel"`
	Agent struct {
		DataDir     string `yaml:"data_dir"`
		HostDataDir string `yaml:"host_data_dir"`
		LogLevel    string `yaml:"log_level"`
	} `yaml:"agent"`
	Runtime struct {
		Type string `yaml:"type"`
	} `yaml:"runtime"`
	OfflineAuth struct {
		Enabled bool   `yaml:"enabled"`
		Listen  string `yaml:"listen"`
	} `yaml:"offline_auth"`
}

// generatedNodeYAML renders a complete node.yaml for the install dialog.
// The node token is deliberately kept in a separate file.
func (s *Server) generatedNodeYAML(n *model.Node, runtime string) string {
	var cfg generatedNodeConfig
	cfg.Version = 1
	cfg.Panel.URL = s.panelBaseURL(context.Background())
	cfg.Panel.TokenFile = "/config/node.token"
	cfg.Agent.DataDir = "/data"
	cfg.Agent.HostDataDir = "/var/lib/mikvace"
	cfg.Agent.LogLevel = "info"
	cfg.Runtime.Type = runtime
	cfg.OfflineAuth.Enabled = true
	cfg.OfflineAuth.Listen = "127.0.0.1:9998"
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Server) panelBaseURL(ctx context.Context) string {
	pub, _ := s.store.GetSetting(ctx, "public_url")
	pub = strings.TrimRight(strings.TrimSpace(pub), "/")
	if pub == "" {
		pub = s.cfg.PublicURL
	}
	return strings.TrimRight(pub, "/")
}
