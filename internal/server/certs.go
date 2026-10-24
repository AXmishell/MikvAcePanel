package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/certmanager"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// pushNodeState pushes the current certificate (if any) and configuration to a
// freshly connected agent. It runs after hello so capabilities are known.
func (s *Server) pushNodeState(nodeID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	n, err := s.store.GetNode(ctx, nodeID)
	if err != nil {
		return
	}
	if strings.EqualFold(n.TLSMode, "managed") && !hasCapability(n.Capabilities, certmanager.CapabilityCSR) {
		msg := "node agent does not support panel-issued certificates; upgrade the agent first"
		_ = s.store.SetNodeConfigError(ctx, nodeID, msg)
		s.store.AddEvent(ctx, "node.config_error", nodeID, "error", msg)
		return
	}
	s.certs.PushExisting(ctx, nodeID)
	s.syncNodeConfig(ctx, n, true)
}

func hasCapability(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

func (s *Server) handleGetNodeCert(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	resp := map[string]any{"status": "none", "busy": s.certs.Busy(id)}
	nc, err := s.store.GetNodeCertificate(r.Context(), id)
	if err == nil {
		resp["node"] = nc
		resp["status"] = nc.Status
		resp["lastError"] = nc.LastError
		if nc.CertID > 0 {
			if cert, err := s.store.GetCertificate(r.Context(), nc.CertID); err == nil {
				resp["certificate"] = certSummary(cert)
			}
		}
	} else if err != store.ErrNotFound {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if man := s.certs.Pending(id); man != nil {
		resp["manual"] = man
	}
	writeJSON(w, http.StatusOK, resp)
}

func certSummary(c *model.Certificate) map[string]any {
	return map[string]any{
		"id": c.ID, "domains": c.Domains, "commonName": c.CommonName,
		"fingerprint": c.Fingerprint, "issuer": c.Issuer,
		"notBefore": c.NotBefore, "notAfter": c.NotAfter,
		"source": c.Source, "status": c.Status, "autoRenew": c.AutoRenew,
		"createdAt": c.CreatedAt, "updatedAt": c.UpdatedAt,
	}
}

func (s *Server) handleRequestNodeCert(w http.ResponseWriter, r *http.Request) {
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
	if !strings.EqualFold(n.TLSMode, "managed") {
		writeError(w, http.StatusBadRequest, "node TLS mode is not managed")
		return
	}
	if strings.TrimSpace(n.Domain) == "" {
		writeError(w, http.StatusBadRequest, "node has no domain")
		return
	}
	if !s.hub.Connected(id) {
		writeError(w, http.StatusConflict, "node agent is offline")
		return
	}
	if s.certs.Busy(id) {
		writeError(w, http.StatusConflict, "certificate request already in progress")
		return
	}
	if !s.hub.SendToNode(id, "cert.renew", map[string]any{"reason": "manual"}) {
		writeError(w, http.StatusConflict, "node agent is offline")
		return
	}
	_ = s.store.SetNodeCertStatus(r.Context(), id, "requesting", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUploadNodeCert(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	var p struct {
		CertPEM string `json:"certPEM"`
		KeyPEM  string `json:"keyPEM"`
	}
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if strings.TrimSpace(p.CertPEM) == "" || strings.TrimSpace(p.KeyPEM) == "" {
		writeError(w, http.StatusBadRequest, "certPEM and keyPEM are required")
		return
	}
	n, err := s.store.GetNode(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	if !strings.EqualFold(n.TLSMode, "managed") {
		writeError(w, http.StatusBadRequest, "switch the node TLS mode to managed before uploading a certificate")
		return
	}
	cert, err := s.certs.Upload(r.Context(), n, p.CertPEM, p.KeyPEM)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.store.AddEvent(r.Context(), "cert.uploaded", id, "info", "节点证书已由管理员上传："+cert.Fingerprint)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "certificate": certSummary(cert)})
}

func (s *Server) handleConfirmNodeCert(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	if err := s.certs.Confirm(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCancelNodeCert(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	if err := s.certs.Cancel(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.store.SetNodeCertStatus(r.Context(), id, "error", "cancelled by administrator")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGetCertSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.certs.LoadSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ca": settings.CA, "email": settings.Email, "provider": settings.Provider,
		"renewDays": settings.RenewDays, "dnsConfigured": settings.Configured,
		"eabKid": settings.EABKID, "eabConfigured": settings.EABConfigured,
	})
}

func (s *Server) handleUpdateCertSettings(w http.ResponseWriter, r *http.Request) {
	var raw struct {
		CA        string          `json:"ca"`
		Email     string          `json:"email"`
		Provider  string          `json:"provider"`
		RenewDays int             `json:"renewDays"`
		DNSConfig json.RawMessage `json:"dnsConfig"`
		EABKID    string          `json:"eabKid"`
		EABHMAC   string          `json:"eabHmac"`
		ClearEAB  bool            `json:"clearEab"`
	}
	if err := decodeJSON(r, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(raw.Provider))
	switch provider {
	case "", "manual", "cloudflare", "alidns", "dnspod":
	default:
		writeError(w, http.StatusBadRequest, "unsupported DNS provider")
		return
	}
	if raw.RenewDays <= 0 {
		raw.RenewDays = certmanager.DefaultRenewDays
	}
	if raw.RenewDays > 90 {
		writeError(w, http.StatusBadRequest, "renewDays must be at most 90")
		return
	}
	var dnsConfig map[string]string
	if len(raw.DNSConfig) > 0 {
		if err := json.Unmarshal(raw.DNSConfig, &dnsConfig); err != nil {
			writeError(w, http.StatusBadRequest, "invalid dnsConfig")
			return
		}
	}
	eabKID := strings.TrimSpace(raw.EABKID)
	eabHMAC := strings.TrimSpace(raw.EABHMAC)
	if certmanager.RequiresEAB(raw.CA) {
		current, _ := s.certs.LoadSettings(r.Context())
		if raw.ClearEAB || (eabKID == "" && current.EABKID == "") || (eabHMAC == "" && current.EABHMAC == "") {
			writeError(w, http.StatusBadRequest, "LiteSSL requires EAB Key ID and HMAC key")
			return
		}
	}
	settings := certmanager.Settings{
		CA: strings.TrimSpace(raw.CA), Email: strings.TrimSpace(raw.Email),
		Provider: provider, RenewDays: raw.RenewDays,
		EABKID: eabKID, EABHMAC: eabHMAC, ClearEAB: raw.ClearEAB,
	}
	if err := s.certs.SaveSettings(r.Context(), settings, dnsConfig); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.AddEvent(r.Context(), "cert.settings_updated", 0, "info", "证书签发设置已更新")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListNodeCerts(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := []map[string]any{}
	for _, n := range nodes {
		item := map[string]any{"nodeId": n.ID, "nodeName": n.Name, "domain": n.Domain, "tlsMode": n.TLSMode}
		if nc, err := s.store.GetNodeCertificate(r.Context(), n.ID); err == nil {
			item["status"] = nc.Status
			item["appliedFingerprint"] = nc.AppliedFingerprint
			item["lastError"] = nc.LastError
			if nc.CertID > 0 {
				if cert, err := s.store.GetCertificate(r.Context(), nc.CertID); err == nil {
					item["notAfter"] = cert.NotAfter
					item["issuer"] = cert.Issuer
				}
			}
		} else {
			item["status"] = "none"
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
