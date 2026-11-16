package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/gorilla/websocket"
)

func (a *Agent) session(ctx context.Context) error {
	target, err := a.wsURL()
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: a.cfg.PanelInsecure},
		Proxy:            http.ProxyFromEnvironment,
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.cfg.Token)
	header.Set("User-Agent", "MikvAce-Node/"+a.cfg.Version)
	conn, resp, err := dialer.DialContext(ctx, target, header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("%s: %w", resp.Status, err)
		}
		return err
	}
	a.mu.Lock()
	a.conn = conn
	a.connected = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.conn == conn {
			a.conn = nil
			a.connected = false
		}
		a.mu.Unlock()
		a.state.set(StateConnecting)
		_ = conn.Close()
	}()
	a.state.set(StateConnected)
	a.log.Info("connected to panel", "url", target)
	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})
	conn.SetPingHandler(func(message string) error {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return conn.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(10*time.Second))
	})
	a.sendHello()
	a.write("users.request", map[string]any{})
	a.resendPending()
	a.maybeRequestCert(context.Background(), "reconnect")

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go a.heartbeatLoop(sessionCtx)
	go a.statsLoop(sessionCtx)
	go a.userSnapshotLoop(sessionCtx)

	for {
		var msg wsMessage
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return err
			}
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		switch msg.Type {
		case "config":
			a.handleConfig(msg.Payload)
		case "command":
			a.handleCommand(msg.Payload)
		case "cert.push":
			a.handleCertPush(msg.Payload)
		case "cert.error":
			var p struct {
				RequestID string `json:"requestId"`
				Error     string `json:"error"`
			}
			_ = json.Unmarshal(msg.Payload, &p)
			a.log.Warn("certificate issuance failed", "error", p.Error)
		case "cert.renew":
			a.requestCertificate("renew", true)
		case "user.snapshot":
			var snap model.UserAuthSnapshot
			if err := json.Unmarshal(msg.Payload, &snap); err != nil {
				a.log.Warn("invalid user auth snapshot", "error", err)
				break
			}
			if a.auth == nil {
				break
			}
			if err := a.auth.SetSnapshot(snap); err != nil {
				a.log.Warn("persist user auth snapshot", "error", err)
			} else {
				a.log.Info("user auth snapshot updated", "users", len(snap.Users), "generatedAt", snap.GeneratedAt)
			}
		case "log.ack":
			var p struct {
				BatchID string `json:"batchId"`
			}
			_ = json.Unmarshal(msg.Payload, &p)
			a.pendingMu.Lock()
			delete(a.pendingLogs, p.BatchID)
			a.pendingMu.Unlock()
		case "stats.ack":
			var p struct {
				BatchID string `json:"batchId"`
			}
			_ = json.Unmarshal(msg.Payload, &p)
			a.pendingMu.Lock()
			delete(a.pendingStats, p.BatchID)
			a.pendingMu.Unlock()
		case "ping":
			_, _ = a.writeRaw(map[string]any{"type": "pong", "payload": map[string]any{"ts": time.Now().Unix()}})
		}
	}
}

func (a *Agent) sendHello() {
	hy2Version, xrayVersion, runtimeName, cores := a.snapshotVersions(true)
	a.configMu.Lock()
	certFP := a.meta.CertFingerprint
	certAfter := a.meta.CertNotAfter
	a.configMu.Unlock()
	caps := []string{CapabilityCSR, CapabilityMultiCore}
	if a.auth != nil && a.auth.Ready() {
		caps = append(caps, "auth.offline.v1")
	}
	a.write("hello", map[string]any{
		"version": a.cfg.Version, "os": osName(), "arch": archName(),
		"hy2Version": hy2Version, "xrayVersion": xrayVersion, "cores": cores,
		"runtime":      runtimeName,
		"state":        a.State(),
		"onlineUsers":  a.onlineCount(),
		"capabilities": caps,
		"cert": map[string]any{
			"fingerprint": certFP,
			"notAfter":    certAfter,
		},
	})
}

// snapshotVersions returns the currently installed core versions. When refresh
// is true each runtime may re-read its binary version, subject to its own
// throttle. Core list is included so hello and heartbeat stay consistent.
func (a *Agent) snapshotVersions(refresh bool) (hy2Version, xrayVersion, runtimeName string, cores []string) {
	runtimes, cores := a.runtimesState()
	if refresh {
		for _, rt := range runtimes {
			rt.RefreshVersion()
		}
	}
	hy2Version, xrayVersion, runtimeName = "unknown", "", "unknown"
	for core, rt := range runtimes {
		if v := rt.Version(); v != "" {
			if core == "xray" {
				xrayVersion = v
			} else {
				hy2Version = v
			}
		}
	}
	if len(cores) > 0 {
		if rt := runtimes[cores[0]]; rt != nil {
			runtimeName = rt.Name()
		}
	}
	return
}

func (a *Agent) onlineCount() int {
	n := 0
	if a.hasCore("hysteria") {
		n += a.stats.OnlineCount()
	}
	if a.hasCore("xray") {
		a.xrayOnlineMu.Lock()
		cutoff := time.Now().Add(-2 * time.Minute)
		for uid, seen := range a.xrayOnline {
			if seen.After(cutoff) {
				n++
			} else {
				delete(a.xrayOnline, uid)
			}
		}
		a.xrayOnlineMu.Unlock()
	}
	return n
}

func (a *Agent) trafficTotals() (int64, int64) {
	var tx, rx int64
	if a.hasCore("hysteria") {
		t, r := a.stats.Totals()
		tx += t
		rx += r
	}
	if a.hasCore("xray") {
		t, r := a.xrayStats.Totals()
		tx += t
		rx += r
	}
	return tx, rx
}

func (a *Agent) write(typ string, payload any) bool {
	msg := map[string]any{"type": typ, "payload": payload}
	ok, _ := a.writeRaw(msg)
	return ok
}

func (a *Agent) writeRaw(msg any) (bool, error) {
	b, err := json.Marshal(msg)
	if err != nil {
		return false, err
	}
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn == nil {
		return false, errors.New("not connected")
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
		_ = conn.Close()
		return false, err
	}
	return true, nil
}

// userSnapshotLoop keeps the offline auth cache reasonably fresh while the
// panel is reachable. User mutations also trigger an immediate push.
