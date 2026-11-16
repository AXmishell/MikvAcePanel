package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	nodesvc "github.com/axmishell/mikvacepanel/internal/app/nodes"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.nodes.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	allowedNodes, restricted := s.apiKeyAllowedNodes(r)
	allowed := map[int64]bool{}
	for _, id := range allowedNodes {
		allowed[id] = true
	}
	metrics, _ := s.store.LatestMetrics(r.Context())
	items := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		if restricted && !allowed[n.ID] {
			continue
		}
		pub := publicNode(&n)
		filterNodeGroupsForRequest(r, pub)
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
	n, err := s.nodes.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	pub := publicNode(n)
	filterNodeGroupsForRequest(r, pub)
	writeJSON(w, http.StatusOK, pub)
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var p nodePayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	settings, _ := s.store.GetSettings(r.Context())
	applyHy2SubscriptionDefaults(&p.Hy2, settings["subscription_hy2_endpoints"])
	result, err := s.nodes.Create(r.Context(), p.input(), p.groups())
	if err != nil {
		writeNodeError(w, err, "node")
		return
	}
	resp := s.nodeResponse(result.Node)
	resp["configRevision"] = int64(0)
	resp["configChanged"] = false
	resp["pushed"] = result.Pushed
	if result.Config != nil {
		resp["configRevision"] = result.Config.Revision
		resp["configChanged"] = result.Config.Revision > 0
		if result.Config.LastError != "" {
			resp["configError"] = result.Config.LastError
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	existing, err := s.nodes.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	var p nodePayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if sc := apiKeyScopeFrom(r); sc != nil && sc.restricted {
		for _, gid := range p.groups() {
			if !sc.groupManageAllowed(gid) {
				writeError(w, http.StatusForbidden, "API key cannot assign group "+strconv.FormatInt(gid, 10))
				return
			}
		}
	}
	saved, cfg, pushed, err := s.nodes.Update(r.Context(), id, p.input(), p.groups())
	if err != nil {
		writeNodeError(w, err, "node")
		return
	}
	resp := s.nodeResponse(saved)
	resp["configRevision"] = int64(0)
	resp["configChanged"] = false
	resp["pushed"] = pushed
	if cfg != nil {
		resp["configRevision"] = cfg.Revision
		resp["configChanged"] = cfg.Revision > existing.ConfigRevision
		if cfg.LastError != "" {
			resp["configError"] = cfg.LastError
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleReorderNodes applies a drag-and-drop ordering. Only ranking weights are
// changed, so no agent push or config revision is required.
func (s *Server) handleReorderNodes(w http.ResponseWriter, r *http.Request) {
	var p struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := s.nodes.Reorder(r.Context(), p.IDs); err != nil {
		writeNodeError(w, err, "node")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	if err := s.nodes.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err, "node")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleResetNodeToken(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	n, token, err := s.nodes.ResetToken(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
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

// writeNodeError maps application errors to HTTP responses.
func writeNodeError(w http.ResponseWriter, err error, operation string) {
	var validation *nodesvc.ValidationError
	if errors.As(err, &validation) {
		writeError(w, http.StatusBadRequest, validation.Message)
		return
	}
	writeStoreError(w, err, operation)
}
