package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/certmanager"
	"github.com/axmishell/mikvacepanel/internal/hy2config"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
	"github.com/axmishell/mikvacepanel/internal/xrayconfig"
)

var portRangePattern = regexp.MustCompile(`^[0-9]{1,5}(-[0-9]{1,5})?(,[0-9]{1,5}(-[0-9]{1,5})?)*$`)

// CapabilityMultiCore is advertised by agents that can apply several core
// configs in one push.
const CapabilityMultiCore = "multi-core.v1"

type nodePayload struct {
	Name             string             `json:"name"`
	Type             string             `json:"type"`
	Flag             string             `json:"flag"`
	IP               string             `json:"ip"`
	Domain           string             `json:"domain"`
	SNI              string             `json:"sni"`
	Listen           string             `json:"listen"`
	Port             int                `json:"port"`
	PortRange        string             `json:"portRange"`
	HopInterval      string             `json:"hopInterval"`
	ObfsType         string             `json:"obfsType"`
	ObfsPassword     string             `json:"obfsPassword"`
	StatsPort        int                `json:"statsPort"`
	SSHPort          int                `json:"sshPort"`
	SSHUser          string             `json:"sshUser"`
	SSHPassword      string             `json:"sshPassword"`
	SSHKey           string             `json:"sshKey"`
	SSHHostKey       string             `json:"sshHostKey"`
	SSHInstallDocker bool               `json:"sshInstallDocker"`
	TLSMode          string             `json:"tlsMode"`
	ACMEEmail        string             `json:"acmeEmail"`
	BandwidthUp      string             `json:"bandwidthUp"`
	BandwidthDown    string             `json:"bandwidthDown"`
	MasqueradeURL    string             `json:"masqueradeUrl"`
	ACL              []string           `json:"acl"`
	Outbounds        []model.Outbound   `json:"outbounds"`
	Xray             model.XraySettings `json:"xray"`
	Hy2              model.Hy2Settings  `json:"hy2"`
	ECHPEM           string             `json:"echPem"`
	ClearECH         bool               `json:"clearEch"`
	Cores            []string           `json:"cores"`
	RuntimeMode      string             `json:"runtimeMode"`
	MaxOnline        int                `json:"maxOnline"`
	Ranking          float64            `json:"ranking"`
	Active           *bool              `json:"active"`
	GroupIDs         []int64            `json:"groupIds"`
	Groups           []int64            `json:"groups"`
}

func (p *nodePayload) groups() []int64 {
	if p.GroupIDs == nil && p.Groups == nil {
		return nil
	}
	out := append([]int64{}, p.GroupIDs...)
	out = append(out, p.Groups...)
	return out
}

// publicNode returns a copy safe to send to the browser: REALITY private keys
// stay on the panel and are preserved on update by matching inbound tags.
func publicNode(n *model.Node) *model.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Cores = n.EffectiveCores()
	c.Xray = n.Xray
	c.Xray.RealityPrivateKey = ""
	c.Hy2 = n.Hy2
	// ECH private keys must never leave the panel; only the client config list
	// (Hy2.ECHConfig) is public.
	c.Hy2.ECHPEM = ""
	c.Xray.Inbounds = make([]model.XrayInbound, len(n.Xray.Inbounds))
	for i := range n.Xray.Inbounds {
		c.Xray.Inbounds[i] = n.Xray.Inbounds[i]
		c.Xray.Inbounds[i].RealityPrivateKey = ""
	}
	return &c
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	metrics, _ := s.store.LatestMetrics(r.Context())
	items := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		pub := publicNode(&n)
		item := map[string]any{"node": pub, "connected": s.hub.Connected(n.ID)}
		// Flatten the node fields for convenient table/card rendering while
		// keeping a nested object for clients that prefer the old shape.
		b, _ := json.Marshal(pub)
		if err := json.Unmarshal(b, &item); err != nil {
			item = map[string]any{"node": pub, "connected": s.hub.Connected(n.ID)}
		}
		item["node"] = pub
		item["connected"] = s.hub.Connected(n.ID)
		item["sshPasswordSet"] = n.SSHPassword != ""
		item["sshKeySet"] = n.SSHKey != ""
		if m, ok := metrics[n.ID]; ok {
			item["metrics"] = m
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	n, err := s.store.GetNode(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	writeJSON(w, http.StatusOK, publicNode(n))
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var p nodePayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	n, err := s.nodeFromPayload(&p, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	token := "mtn_" + store.RandomToken(24)
	hash := sha256Hex(token)
	n.Token = token
	created, err := s.store.CreateNode(r.Context(), n, hash, token[:12], p.groups())
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	cfg, pushed := s.syncNodeConfig(r.Context(), created, false)
	fresh, _ := s.store.GetNode(r.Context(), created.ID)
	if fresh == nil {
		fresh = created
	}
	resp := s.nodeResponse(fresh)
	resp["configRevision"] = cfg.Revision
	resp["configChanged"] = cfg.Revision > 0
	resp["pushed"] = pushed
	if cfg.LastError != "" {
		resp["configError"] = cfg.LastError
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	existing, err := s.store.GetNode(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	var p nodePayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	updated, err := s.nodeFromPayload(&p, existing)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.store.UpdateNode(r.Context(), updated, p.groups())
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	cfg, pushed := s.syncNodeConfig(r.Context(), saved, false)
	if saved.HasCore("hysteria") {
		s.sendUserSnapshot(r.Context(), saved.ID)
	}
	if fresh, err := s.store.GetNode(r.Context(), saved.ID); err == nil {
		saved = fresh
	}
	resp := s.nodeResponse(saved)
	resp["configRevision"] = cfg.Revision
	resp["configChanged"] = cfg.Revision > existing.ConfigRevision
	resp["pushed"] = pushed
	if cfg.LastError != "" {
		resp["configError"] = cfg.LastError
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	n, _ := s.store.GetNode(r.Context(), id)
	s.hub.Disconnect(id)
	if err := s.store.DeleteNode(r.Context(), id); err != nil {
		writeStoreError(w, err, "node")
		return
	}
	if n != nil {
		s.store.AddEvent(r.Context(), "node.deleted", 0, "info", "节点 "+n.Name+" 已删除")
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleResetNodeToken(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	n, err := s.store.GetNode(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	token := "mtn_" + store.RandomToken(24)
	if err := s.store.ResetNodeToken(r.Context(), id, token, sha256Hex(token), token[:12]); err != nil {
		writeStoreError(w, err, "node")
		return
	}
	s.hub.Disconnect(id)
	n.Token = token
	pub := s.panelBaseURL(r.Context())
	mode := nodeRuntime(n)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token, "tokenPrefix": token[:12], "runtimeMode": mode,
		"installScript": fmt.Sprintf("curl -fsSL %s/api/agent/install.sh | sudo bash -s -- --panel %q --token %q --runtime %s",
			pub, pub, n.Token, mode),
		"installCommand": s.installCommand(n, mode),
		"processCommand": s.installCommand(n, "process"),
		"dockerCommand":  s.installCommand(n, "docker"),
		"compose":        s.generatedNodeCompose(n, mode),
	})
}

func (s *Server) handleSyncNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	n, err := s.store.GetNode(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	before := n.ConfigRevision
	cfg, pushed := s.syncNodeConfig(r.Context(), n, true)
	resp := map[string]any{
		"ok": true, "revision": cfg.Revision, "pushed": pushed,
		"configChanged": cfg.Revision > before,
		"connected":     s.hub.Connected(id),
	}
	if cfg.LastError != "" {
		resp["configError"] = cfg.LastError
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSyncAll(w http.ResponseWriter, r *http.Request) {
	total, pushed := s.syncAllNodes(r.Context())
	s.store.AddEvent(r.Context(), "sync.completed", 0, "info",
		fmt.Sprintf("已同步 %d 个节点（推送 %d 份配置）", total, pushed))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nodes": total, "pushed": pushed})
}

func (s *Server) handleNodeCommand(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	switch req.Action {
	case "start", "stop", "restart", "sync":
	default:
		writeError(w, http.StatusBadRequest, "unsupported action")
		return
	}
	if req.Action == "sync" {
		n, err := s.store.GetNode(r.Context(), id)
		if err != nil {
			writeStoreError(w, err, "node")
			return
		}
		cfg, pushed := s.syncNodeConfig(r.Context(), n, true)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision": cfg.Revision, "pushed": pushed})
		return
	}
	cmdID, ok := s.hub.SendCommand(id, req.Action)
	if !ok {
		writeError(w, http.StatusConflict, "node agent is not connected")
		return
	}
	s.store.AddEvent(r.Context(), "node.command", id, "info", "已向节点发送命令 "+req.Action)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commandId": cmdID})
}

func (s *Server) handleNodeConfig(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	cfg, err := s.store.GetNodeConfig(r.Context(), id, 0)
	if err == store.ErrNotFound {
		writeJSON(w, http.StatusOK, map[string]any{"yaml": "", "revision": 0})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cores := []string{"hysteria"}
	protocol := "hysteria"
	if n, nerr := s.store.GetNode(r.Context(), id); nerr == nil {
		protocol = n.PrimaryCore()
		cores = n.EffectiveCores()
	}
	if len(cfg.Configs) == 0 {
		cfg.Configs = map[string]string{protocol: cfg.YAML}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": cfg.ID, "nodeId": cfg.NodeID, "revision": cfg.Revision, "yaml": cfg.YAML,
		"configs": cfg.Configs, "cores": cores,
		"hash": cfg.Hash, "createdAt": cfg.CreatedAt, "appliedAt": cfg.AppliedAt,
		"lastError": cfg.LastError, "protocol": protocol,
	})
}

func nodeRuntime(n *model.Node) string {
	if n != nil && strings.EqualFold(strings.TrimSpace(n.RuntimeMode), "docker") {
		return "docker"
	}
	return "process"
}

func (s *Server) handleNodeInstall(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	n, err := s.store.GetNode(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	pub := s.panelBaseURL(r.Context())
	mode := nodeRuntime(n)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":       n.Token,
		"publicUrl":   pub,
		"runtimeMode": mode,
		"installScript": fmt.Sprintf("curl -fsSL %s/api/agent/install.sh | sudo bash -s -- --panel %q --token %q --runtime %s",
			pub, pub, n.Token, mode),
		"installCommand": s.installCommand(n, mode),
		"processCommand": s.installCommand(n, "process"),
		"dockerCommand":  s.installCommand(n, "docker"),
		"compose":        s.generatedNodeCompose(n, mode),
	})
}

func (s *Server) installCommand(n *model.Node, runtime string) string {
	env := fmt.Sprintf("  -e PANEL_URL=%s \\\n  -e NODE_TOKEN=%s \\\n  -e HY2_RUNTIME=%s \\\n",
		shellQuote(s.panelBaseURL(context.Background())), shellQuote(n.Token), shellQuote(runtime))
	extra := ""
	if runtime == "docker" {
		env += fmt.Sprintf("  -e HOST_DATA_DIR=%s \\\n", shellQuote(nodeDataDir))
		extra = "  -v /var/run/docker.sock:/var/run/docker.sock \\\n"
	}
	return fmt.Sprintf(`sudo install -d -m 700 %s
docker rm -f mikvace-node >/dev/null 2>&1 || true
docker run -d --name mikvace-node --restart unless-stopped \
  --network host --cap-add NET_ADMIN \
%s%s  -v %s:/data \
  ghcr.io/axmishell/mikvacepanel-node:latest`, nodeDataDir, env, extra, nodeDataDir)
}

func (s *Server) nodeFromPayload(p *nodePayload, existing *model.Node) (*model.Node, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, fmt.Errorf("node name is required")
	}
	nodeType := strings.ToLower(strings.TrimSpace(p.Type))
	if nodeType == "" {
		if len(p.Cores) > 0 {
			nodeType = strings.ToLower(strings.TrimSpace(p.Cores[0]))
		} else if existing != nil && existing.Type != "" {
			nodeType = existing.Type
		} else {
			nodeType = "hysteria"
		}
	}
	if nodeType != "hysteria" && nodeType != "xray" {
		return nil, fmt.Errorf("node type must be hysteria or xray")
	}
	cores := []string{}
	seenCores := map[string]bool{}
	for _, core := range p.Cores {
		core = strings.ToLower(strings.TrimSpace(core))
		if core == "" || seenCores[core] {
			continue
		}
		if !model.ValidCore(core) {
			return nil, fmt.Errorf("unsupported core %q", core)
		}
		seenCores[core] = true
		cores = append(cores, core)
	}
	if len(cores) == 0 {
		cores = []string{nodeType}
	}
	// The first core is the node's primary type; it keeps legacy endpoints and
	// the Type column meaningful while Cores drives multi-core rendering.
	nodeType = cores[0]
	port := p.Port
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	tlsMode := strings.ToLower(strings.TrimSpace(p.TLSMode))
	if tlsMode == "" {
		tlsMode = "selfsigned"
	}
	if tlsMode != "selfsigned" && tlsMode != "acme" && tlsMode != "managed" {
		return nil, fmt.Errorf("tlsMode must be selfsigned, acme or managed")
	}
	domain := strings.TrimSpace(p.Domain)
	if (tlsMode == "acme" || tlsMode == "managed") && domain == "" {
		return nil, fmt.Errorf("ACME or panel-issued certificates require a domain")
	}
	portRange := strings.TrimSpace(p.PortRange)
	if portRange != "" {
		if !portRangePattern.MatchString(portRange) {
			return nil, fmt.Errorf("port range must look like 20000-50000")
		}
		if err := validatePortRange(portRange); err != nil {
			return nil, err
		}
	}
	obfs := strings.ToLower(strings.TrimSpace(p.ObfsType))
	if obfs != "" && obfs != "salamander" && obfs != "gecko" {
		return nil, fmt.Errorf("obfs type must be salamander or gecko")
	}
	if obfs != "" && strings.TrimSpace(p.ObfsPassword) == "" {
		return nil, fmt.Errorf("obfs password is required when obfuscation is enabled")
	}
	statsPort := p.StatsPort
	if statsPort == 0 {
		statsPort = 9999
	}
	if statsPort < 1 || statsPort > 65535 {
		return nil, fmt.Errorf("stats port must be between 1 and 65535")
	}
	ranking := p.Ranking
	if ranking == 0 {
		ranking = 1
	}
	active := true
	if p.Active != nil {
		active = *p.Active
	}
	acl := []string{}
	for _, line := range p.ACL {
		line = strings.TrimSpace(line)
		if line != "" {
			acl = append(acl, line)
		}
	}
	if len(acl) > 500 {
		return nil, fmt.Errorf("too many ACL rules (max 500)")
	}
	outbounds := []model.Outbound{}
	for _, o := range p.Outbounds {
		o.Name = strings.TrimSpace(o.Name)
		o.Type = strings.ToLower(strings.TrimSpace(o.Type))
		if o.Name == "" {
			continue
		}
		if o.Type != "direct" && o.Type != "socks5" && o.Type != "http" {
			return nil, fmt.Errorf("outbound %q has unsupported type", o.Name)
		}
		outbounds = append(outbounds, o)
	}
	hy2, err := normalizeHy2(p, existing)
	if err != nil {
		return nil, err
	}
	if err := validateHy2(&hy2, existing, tlsMode, portRange, obfs, cores, p.Xray.Inbounds); err != nil {
		return nil, err
	}
	sshPort := p.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}
	if sshPort < 1 || sshPort > 65535 {
		return nil, fmt.Errorf("SSH port must be between 1 and 65535")
	}
	runtimeMode := strings.ToLower(strings.TrimSpace(p.RuntimeMode))
	if runtimeMode == "" {
		if existing != nil && existing.RuntimeMode != "" {
			runtimeMode = existing.RuntimeMode
		} else {
			runtimeMode = "process"
		}
	}
	if runtimeMode != "process" && runtimeMode != "docker" {
		return nil, fmt.Errorf("runtimeMode must be process or docker")
	}
	n := &model.Node{
		Name: name, Type: nodeType, Flag: strings.TrimSpace(p.Flag),
		IP: strings.TrimSpace(p.IP), Domain: domain, SNI: strings.TrimSpace(p.SNI),
		Listen: strings.TrimSpace(p.Listen), Port: port, PortRange: portRange,
		HopInterval: strings.TrimSpace(p.HopInterval), ObfsType: obfs, ObfsPassword: p.ObfsPassword,
		StatsPort: statsPort, SSHPort: sshPort, SSHUser: strings.TrimSpace(p.SSHUser),
		SSHPassword: p.SSHPassword, SSHKey: p.SSHKey, SSHHostKey: strings.TrimSpace(p.SSHHostKey),
		SSHInstallDocker: p.SSHInstallDocker,
		TLSMode:          tlsMode, ACMEEmail: strings.TrimSpace(p.ACMEEmail),
		BandwidthUp: strings.TrimSpace(p.BandwidthUp), BandwidthDown: strings.TrimSpace(p.BandwidthDown),
		MasqueradeURL: strings.TrimSpace(p.MasqueradeURL), ACL: acl, Outbounds: outbounds,
		Xray: p.Xray, Hy2: hy2, Cores: cores, MaxOnline: p.MaxOnline, Ranking: ranking, Active: active,
		RuntimeMode: runtimeMode,
	}
	if n.HasCore("xray") {
		if existing != nil && existing.HasCore("xray") {
			if n.Xray.RealityPrivateKey == "" && existing.Xray.RealityPrivateKey != "" {
				n.Xray.RealityPrivateKey = existing.Xray.RealityPrivateKey
				n.Xray.RealityPublicKey = existing.Xray.RealityPublicKey
			}
			if len(n.Xray.RealityShortIDs) == 0 {
				n.Xray.RealityShortIDs = existing.Xray.RealityShortIDs
			}
			// Preserve REALITY material of inbounds that were left unchanged.
			for i := range n.Xray.Inbounds {
				for _, old := range existing.Xray.EffectiveInbounds() {
					if old.Tag == n.Xray.Inbounds[i].Tag && n.Xray.Inbounds[i].RealityPrivateKey == "" {
						n.Xray.Inbounds[i].RealityPrivateKey = old.RealityPrivateKey
						n.Xray.Inbounds[i].RealityPublicKey = old.RealityPublicKey
						n.Xray.Inbounds[i].RealityShortIDs = old.RealityShortIDs
					}
				}
			}
		}
		if err := xrayconfig.Normalize(n); err != nil {
			return nil, err
		}
		if n.Xray.Security == "tls" && n.TLSMode == "acme" && len(cores) == 1 {
			// A pure-Xray node has no built-in ACME in this panel; panel-issued
			// managed certificates are the supported option.
			n.TLSMode = "selfsigned"
		}
		if len(cores) == 1 {
			n.Port = n.Xray.Inbounds[0].Port
		}
	}
	if existing != nil {
		n.ID = existing.ID
		n.StatsSecret = existing.StatsSecret
		n.Token = existing.Token
		if n.SSHPassword == "" {
			n.SSHPassword = existing.SSHPassword
		}
		if n.SSHKey == "" {
			n.SSHKey = existing.SSHKey
		}
		if n.SSHHostKey == "" {
			n.SSHHostKey = existing.SSHHostKey
		}
	}
	if n.StatsSecret == "" {
		n.StatsSecret = store.RandomToken(16)
	}
	return n, nil
}

func (s *Server) nodeResponse(n *model.Node) map[string]any {
	mode := nodeRuntime(n)
	return map[string]any{
		"node":           publicNode(n),
		"sshPasswordSet": n.SSHPassword != "",
		"sshKeySet":      n.SSHKey != "",
		"runtimeMode":    mode,
		"installCommand": s.installCommand(n, mode),
		"processCommand": s.installCommand(n, "process"),
		"dockerCommand":  s.installCommand(n, "docker"),
		"compose":        s.generatedNodeCompose(n, mode),
		"connected":      s.hub.Connected(n.ID),
	}
}

// syncNodeConfig renders and stores a node configuration, then pushes it when
// force is true or the agent is connected and has not applied it yet.
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
		if hasCapability(n.Capabilities, CapabilityMultiCore) {
			pushed = s.hub.PushConfigBundle(n.ID, cfg.Revision, configs, cfg.Cores, n.TLSMode, n.Domain, host, agentPortRange, n.Port, files)
		} else {
			// Older agents only understand a single core; keep them working
			// with the node's primary core.
			pushed = s.hub.PushConfig(n.ID, cfg.Revision, primary, configs[primary], n.TLSMode, n.Domain, host, agentPortRange, n.Port, files)
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

func validatePortRange(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		segs := strings.SplitN(part, "-", 2)
		start, err := strconv.Atoi(segs[0])
		if err != nil || start < 1 || start > 65535 {
			return fmt.Errorf("invalid port range %q", part)
		}
		if len(segs) == 2 {
			end, err := strconv.Atoi(segs[1])
			if err != nil || end < start || end > 65535 {
				return fmt.Errorf("invalid port range %q", part)
			}
		}
	}
	return nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
