package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/certmanager"
	"github.com/axmishell/mikvacepanel/internal/hy2config"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/xrayconfig"
)

func (s *Server) syncNodeConfig(ctx context.Context, n *model.Node, force bool) (*model.NodeConfig, bool) {
	// Serialize config generation: user edits, quota reconciliation and agent
	// reconnects can otherwise race for the next revision number.
	s.configSyncMu.Lock()
	defer s.configSyncMu.Unlock()
	pub, _ := s.store.GetSetting(ctx, "public_url")
	pub = strings.TrimRight(pub, "/")
	if pub == "" {
		pub = s.cfg.PublicURL
	}
	configs, hash, err := s.renderNodeConfigs(ctx, n, pub)
	if err != nil {
		_ = s.store.SetNodeConfigError(ctx, n.ID, err.Error())
		s.store.AddEvent(ctx, "node.config_error", n.ID, "error", "配置生成失败："+err.Error())
		return &model.NodeConfig{NodeID: n.ID, Revision: n.ConfigRevision, LastError: err.Error()}, false
	}
	primary := n.PrimaryCore()
	if len(n.EffectiveCores()) > 1 && s.hub.Connected(n.ID) && !hasCapability(n.Capabilities, CapabilityMultiCore) {
		msg := "node agent does not support multi-core; upgrade the agent first"
		_ = s.store.SetNodeConfigError(ctx, n.ID, msg)
		return &model.NodeConfig{NodeID: n.ID, Revision: n.ConfigRevision, LastError: msg}, false
	}
	if strings.EqualFold(n.TLSMode, "managed") && s.hub.Connected(n.ID) && !hasCapability(n.Capabilities, certmanager.CapabilityCSR) {
		msg := "node agent does not support panel-issued certificates; upgrade the agent first"
		_ = s.store.SetNodeConfigError(ctx, n.ID, msg)
		return &model.NodeConfig{NodeID: n.ID, Revision: n.ConfigRevision, LastError: msg}, false
	}
	cfg, created, err := s.store.SaveNodeConfigBundle(ctx, n.ID, configs[primary], configs, hash)
	if err != nil {
		s.log.Error("save node config", "node", n.ID, "error", err)
		return &model.NodeConfig{NodeID: n.ID, Revision: n.ConfigRevision}, false
	}
	cfg.Cores = n.EffectiveCores()
	pushed := false
	if s.hub.Connected(n.ID) && (force || created || n.AppliedRevision < cfg.Revision) {
		host := n.Domain
		if host == "" {
			host = n.IP
		}
		// The built-in Hysteria listen range, Realms mode and mimic replace
		// the agent managed iptables port hopping.
		agentPortRange := n.PortRange
		if n.Hy2.ListenPortRange != "" || n.Hy2.IsRealm() || (n.Hy2.Mimic != nil && n.Hy2.Mimic.Enabled) {
			agentPortRange = ""
		}
		files := map[string]string{}
		if n.Hy2.ECHPEM != "" {
			files["ech"] = n.Hy2.ECHPEM
		}
		if n.Hy2.ClientCA != "" {
			files["clientCA"] = n.Hy2.ClientCA
		}
		// Port hopping redirects to the port Hysteria actually listens on.
		// The node port and the listen address may differ on legacy rows.
		listenPort := n.EffectiveListenPort()
		if hasCapability(n.Capabilities, CapabilityMultiCore) {
			pushed = s.hub.PushConfigBundle(n.ID, cfg.Revision, configs, cfg.Cores, n.TLSMode, n.Domain, host, agentPortRange, listenPort, files)
		} else {
			// Older agents only understand a single core; keep them working
			// with the node's primary core.
			pushed = s.hub.PushConfig(n.ID, cfg.Revision, primary, configs[primary], n.TLSMode, n.Domain, host, agentPortRange, listenPort, files)
		}
	}
	return cfg, pushed
}

// renderNodeConfigs renders one configuration per enabled core.
func (s *Server) renderNodeConfigs(ctx context.Context, n *model.Node, publicURL string) (map[string]string, string, error) {
	configs := map[string]string{}
	for _, core := range n.EffectiveCores() {
		switch core {
		case "hysteria":
			text, _, err := hy2config.Render(n, publicURL)
			if err != nil {
				return nil, "", fmt.Errorf("hysteria config: %w", err)
			}
			configs[core] = text
		case "xray":
			users, err := s.store.UsersForNode(ctx, n.ID)
			if err != nil {
				return nil, "", err
			}
			text, _, err := xrayconfig.Render(n, users)
			if err != nil {
				return nil, "", fmt.Errorf("xray config: %w", err)
			}
			configs[core] = text
		default:
			return nil, "", fmt.Errorf("unsupported core %q", core)
		}
	}
	if len(configs) == 0 {
		return nil, "", fmt.Errorf("node has no enabled cores")
	}
	if len(configs) == 1 {
		// Keep the single-core hash stable across the multi-core upgrade so
		// existing nodes are not needlessly restarted.
		for _, text := range configs {
			sum := sha256.Sum256([]byte(text))
			return configs, hex.EncodeToString(sum[:]), nil
		}
	}
	return configs, hashConfigs(configs), nil
}

func hashConfigs(configs map[string]string) string {
	cores := make([]string, 0, len(configs))
	for core := range configs {
		cores = append(cores, core)
	}
	sort.Strings(cores)
	h := sha256.New()
	for _, core := range cores {
		h.Write([]byte(core))
		h.Write([]byte{0})
		h.Write([]byte(configs[core]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// syncXrayNodes re-renders every active Xray node so user membership changes
// take effect immediately.
func (s *Server) syncXrayNodes(ctx context.Context) {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return
	}
	for i := range nodes {
		if nodes[i].HasCore("xray") {
			s.syncNodeConfig(ctx, &nodes[i], false)
		}
	}
	// Hysteria users are not embedded in the config, so refresh the offline
	// auth cache separately after every user/group change.
	s.pushHysteriaAuthSnapshots(ctx)
}
