package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// Agent is the node side controller.
type Agent struct {
	cfg Config
	log *slog.Logger

	runtime      Runtime
	protocol     string
	stats        *statsPoller
	xrayStats    *xrayStatsPoller
	sampler      sampler
	logCh        chan LogLine
	tailMu       sync.Mutex
	tailStop     context.CancelFunc
	xrayOnlineMu sync.Mutex
	xrayOnline   map[string]time.Time

	mu        sync.Mutex
	conn      *websocket.Conn
	writeMu   sync.Mutex
	closed    bool
	connected bool

	pendingMu    sync.Mutex
	pendingLogs  map[string]*model.LogBatch
	pendingStats map[string]*model.StatsBatch

	configMu        sync.Mutex
	currentConfig   string
	currentRevision int64
	appliedRevision int64
	meta            nodeMeta
}

type nodeMeta struct {
	Revision  int64  `json:"revision"`
	Protocol  string `json:"protocol"`
	TLSMode   string `json:"tlsMode"`
	Domain    string `json:"domain"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	PortRange string `json:"portRange"`
}

type wsMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// New creates an agent.
func New(cfg Config, logger *slog.Logger) (*Agent, error) {
	a := &Agent{
		cfg: cfg, log: logger,
		logCh:        make(chan LogLine, 2048),
		xrayOnline:   map[string]time.Time{},
		pendingLogs:  map[string]*model.LogBatch{},
		pendingStats: map[string]*model.StatsBatch{},
	}
	a.loadLocalState()
	protocol := a.meta.Protocol
	if protocol != "" {
		rt, err := NewRuntime(cfg, protocol, a.enqueueLog)
		if err != nil {
			return nil, err
		}
		a.runtime = rt
		a.protocol = protocol
	}
	a.stats = newStatsPoller("", "127.0.0.1:9999")
	a.xrayStats = newXrayStatsPoller(cfg.XrayBin)
	return a, nil
}

func (a *Agent) ensureRuntime(protocol string) error {
	if protocol == "" {
		protocol = "hysteria"
	}
	if a.runtime != nil && a.protocol == protocol {
		return nil
	}
	if a.runtime != nil {
		a.runtime.Close()
	}
	rt, err := NewRuntime(a.cfg, protocol, a.enqueueLog)
	if err != nil {
		return err
	}
	a.runtime = rt
	a.protocol = protocol
	return nil
}

// Run starts the node agent and blocks until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	if a.runtime != nil {
		defer a.runtime.Close()
	}
	defer a.stopAccessTail()
	go a.logBatcher(ctx)
	go a.retransmitLoop(ctx)
	if a.currentConfig != "" && a.runtime != nil {
		if a.protocol == "xray" {
			a.xrayStats.Configure(a.currentConfig)
			a.startAccessTail(a.accessLogPath())
		} else {
			a.stats.Configure(a.currentConfig)
			if a.meta.PortRange != "" {
				if err := SetupPortHopping(a.meta.Port, a.meta.PortRange); err != nil {
					a.log.Warn("port hopping setup", "error", err)
				}
			}
		}
		if err := a.runtime.Start(); err != nil {
			a.log.Warn("start "+a.protocol+" from local config", "error", err)
		}
	}
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		started := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			a.log.Warn("control channel disconnected", "error", err)
		}
		if time.Since(started) > 30*time.Second {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (a *Agent) wsURL() (string, error) {
	u, err := url.Parse(a.cfg.PanelURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		if strings.HasPrefix(a.cfg.PanelURL, "wss://") || strings.HasPrefix(a.cfg.PanelURL, "ws://") {
			// already a websocket URL; use as is below
		} else {
			return "", fmt.Errorf("panel URL must start with http:// or https://")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/node/ws"
	u.RawQuery = ""
	return u.String(), nil
}

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
		_ = conn.Close()
	}()
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
	a.resendPending()

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go a.heartbeatLoop(sessionCtx)
	go a.statsLoop(sessionCtx)

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
	version, runtimeName := "unknown", "unknown"
	if a.runtime != nil {
		version, runtimeName = a.runtime.Version(), a.runtime.Name()
	}
	a.write("hello", map[string]any{
		"version": a.cfg.Version, "os": osName(), "arch": archName(),
		"hy2Version": version, "runtime": runtimeName,
		"onlineUsers": a.onlineCount(),
	})
}

func (a *Agent) onlineCount() int {
	if a.runtime == nil {
		return 0
	}
	if a.protocol == "xray" {
		a.xrayOnlineMu.Lock()
		defer a.xrayOnlineMu.Unlock()
		cutoff := time.Now().Add(-2 * time.Minute)
		n := 0
		for uid, seen := range a.xrayOnline {
			if seen.After(cutoff) {
				n++
			} else {
				delete(a.xrayOnline, uid)
			}
		}
		return n
	}
	return a.stats.OnlineCount()
}

func (a *Agent) trafficTotals() (int64, int64) {
	if a.protocol == "xray" {
		return a.xrayStats.Totals()
	}
	return a.stats.Totals()
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

func (a *Agent) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sys := a.sampler.Sample(a.cfg.DataDir)
			tx, rx := a.trafficTotals()
			a.write("heartbeat", map[string]any{
				"cpuPct": sys.CPUPct, "memUsed": sys.MemUsed, "memTotal": sys.MemTotal,
				"diskUsed": sys.DiskUsed, "diskTotal": sys.DiskTotal, "load1": sys.Load1,
				"uptimeSec": sys.UptimeSec, "online": a.onlineCount(),
				"trafficTx": tx, "trafficRx": rx, "hy2Running": a.runtime.Running(),
			})
		}
	}
}

func (a *Agent) statsLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.StatsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var batch *model.StatsBatch
			if a.protocol == "xray" {
				batch = a.xrayStats.Poll(ctx)
			} else {
				batch = a.stats.Poll(ctx)
			}
			if batch == nil {
				continue
			}
			a.pendingMu.Lock()
			if len(a.pendingStats) > 100 {
				for k := range a.pendingStats {
					delete(a.pendingStats, k)
					break
				}
			}
			a.pendingStats[batch.BatchID] = batch
			a.pendingMu.Unlock()
			a.write("stats", batch)
		}
	}
}

func (a *Agent) retransmitLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			connected := a.connected
			a.mu.Unlock()
			if !connected {
				continue
			}
			a.pendingMu.Lock()
			logs := make([]*model.LogBatch, 0, len(a.pendingLogs))
			for _, b := range a.pendingLogs {
				logs = append(logs, b)
			}
			stats := make([]*model.StatsBatch, 0, len(a.pendingStats))
			for _, b := range a.pendingStats {
				stats = append(stats, b)
			}
			a.pendingMu.Unlock()
			for _, b := range logs {
				a.write("logs", b)
			}
			for _, b := range stats {
				a.write("stats", b)
			}
		}
	}
}

func (a *Agent) resendPending() {
	a.pendingMu.Lock()
	logs := make([]*model.LogBatch, 0, len(a.pendingLogs))
	for _, b := range a.pendingLogs {
		logs = append(logs, b)
	}
	stats := make([]*model.StatsBatch, 0, len(a.pendingStats))
	for _, b := range a.pendingStats {
		stats = append(stats, b)
	}
	a.pendingMu.Unlock()
	for _, b := range logs {
		a.write("logs", b)
	}
	for _, b := range stats {
		a.write("stats", b)
	}
}

func (a *Agent) enqueueLog(line LogLine) {
	if strings.Contains(line.Raw, " accepted ") {
		if idx := strings.Index(line.Raw, "email: "); idx > 0 {
			uid := strings.TrimSpace(line.Raw[idx+7:])
			if uid != "" {
				a.xrayOnlineMu.Lock()
				a.xrayOnline[uid] = time.Now()
				a.xrayOnlineMu.Unlock()
			}
		}
	}
	select {
	case a.logCh <- line:
	default:
		// Drop under pressure; the agent never blocks the Hysteria process.
	}
}

func (a *Agent) logBatcher(ctx context.Context) {
	var batch []model.IncomingLog
	flush := func() {
		if len(batch) == 0 {
			return
		}
		lb := &model.LogBatch{BatchID: randomID(), Entries: batch}
		batch = nil
		a.pendingMu.Lock()
		if len(a.pendingLogs) > 200 {
			for k := range a.pendingLogs {
				delete(a.pendingLogs, k)
				break
			}
		}
		a.pendingLogs[lb.BatchID] = lb
		a.pendingMu.Unlock()
		a.write("logs", lb)
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	defer flush()
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case line := <-a.logCh:
			batch = append(batch, model.IncomingLog{TS: line.TS, Level: line.Level, Message: line.Message, Raw: line.Raw})
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (a *Agent) handleConfig(raw json.RawMessage) {
	var msg struct {
		Revision  int64  `json:"revision"`
		Protocol  string `json:"protocol"`
		Config    string `json:"config"`
		YAML      string `json:"yaml"`
		TLSMode   string `json:"tlsMode"`
		Domain    string `json:"domain"`
		Host      string `json:"host"`
		Port      int    `json:"port"`
		PortRange string `json:"portRange"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		a.log.Warn("invalid config push")
		return
	}
	configText := msg.Config
	if strings.TrimSpace(configText) == "" {
		configText = msg.YAML
	}
	if strings.TrimSpace(configText) == "" {
		a.log.Warn("invalid config push")
		return
	}
	protocol := strings.ToLower(strings.TrimSpace(msg.Protocol))
	if protocol == "" {
		protocol = "hysteria"
	}
	a.configMu.Lock()
	if a.runtime != nil && msg.Revision == a.appliedRevision && configText == a.currentConfig && a.protocol == protocol && a.runtime.Running() {
		a.configMu.Unlock()
		a.write("config.ack", map[string]any{"revision": msg.Revision, "ok": true})
		return
	}
	previous := a.currentConfig
	previousProtocol := a.protocol
	a.configMu.Unlock()

	if err := a.applyConfig(msg.Revision, protocol, configText, msg.TLSMode, msg.Domain, msg.Host, msg.Port, msg.PortRange); err != nil {
		a.log.Error("apply config failed", "revision", msg.Revision, "error", err)
		// Restore the previous configuration so the node keeps serving.
		if a.protocol == protocol {
			RemovePortHopping(msg.Port, msg.PortRange)
		}
		if previous != "" {
			if a.protocol != previousProtocol {
				if err := a.ensureRuntime(previousProtocol); err != nil {
					a.log.Error("restore runtime", "error", err)
				}
			}
			_ = a.writeConfigFile(previous)
			if previousProtocol == "xray" {
				a.xrayStats.Configure(previous)
			} else {
				a.stats.Configure(previous)
			}
			_ = a.runtime.Restart()
			if a.meta.PortRange != "" && previousProtocol != "xray" {
				_ = SetupPortHopping(a.meta.Port, a.meta.PortRange)
			}
		}
		a.write("config.ack", map[string]any{"revision": msg.Revision, "ok": false, "error": err.Error()})
		return
	}
	a.log.Info("configuration applied", "revision", msg.Revision)
	a.write("config.ack", map[string]any{"revision": msg.Revision, "ok": true})
}

func (a *Agent) applyConfig(revision int64, protocol, configText, tlsMode, domain, host string, port int, portRange string) error {
	if err := a.ensureRuntime(protocol); err != nil {
		return err
	}
	if tlsMode != "acme" {
		if err := EnsureSelfSignedCert(a.cfg.DataDir, host); err != nil {
			return fmt.Errorf("certificate: %w", err)
		}
	}
	prepared := PrepareConfig(a.cfg, configText)
	if err := a.writeConfigFile(prepared); err != nil {
		return err
	}
	if protocol == "xray" {
		a.xrayStats.Configure(prepared)
	} else {
		a.stats.Configure(prepared)
	}
	prev := a.meta
	if protocol != "xray" {
		if prev.PortRange != portRange || prev.Port != port {
			RemovePortHopping(prev.Port, prev.PortRange)
		}
		if err := SetupPortHopping(port, portRange); err != nil {
			a.log.Warn("port hopping setup failed", "error", err)
		}
	} else {
		RemovePortHopping(prev.Port, prev.PortRange)
	}
	if err := a.runtime.Restart(); err != nil {
		return fmt.Errorf("start %s: %w", protocol, err)
	}
	// Give the process a moment to validate the config and bind its port.
	time.Sleep(1500 * time.Millisecond)
	if !a.runtime.Running() {
		return fmt.Errorf("%s exited after applying the configuration", protocol)
	}
	a.configMu.Lock()
	a.currentConfig = prepared
	a.currentRevision = revision
	a.appliedRevision = revision
	a.meta = nodeMeta{Revision: revision, Protocol: protocol, TLSMode: tlsMode, Domain: domain, Host: host, Port: port, PortRange: portRange}
	a.configMu.Unlock()
	a.saveLocalState()
	if protocol == "xray" {
		a.startAccessTail(a.accessLogPath())
	} else {
		a.stopAccessTail()
	}
	a.sendHello()
	return nil
}

func (a *Agent) writeConfigFile(content string) error {
	dir := filepath.Join(a.cfg.DataDir, "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := a.runtime.ConfigPath()
	if err := os.WriteFile(path+".tmp", []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (a *Agent) handleCommand(raw json.RawMessage) {
	var cmd struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return
	}
	var err error
	if a.runtime == nil {
		a.write("command.ack", map[string]any{"id": cmd.ID, "action": cmd.Action, "ok": false, "output": "no configuration applied yet"})
		return
	}
	switch cmd.Action {
	case "start":
		err = a.runtime.Start()
	case "stop":
		err = a.runtime.Stop()
	case "restart":
		err = a.runtime.Restart()
	default:
		err = fmt.Errorf("unknown action %q", cmd.Action)
	}
	payload := map[string]any{"id": cmd.ID, "action": cmd.Action, "ok": err == nil}
	if err != nil {
		payload["output"] = err.Error()
	} else {
		payload["output"] = "ok"
	}
	a.write("command.ack", payload)
}

func (a *Agent) loadLocalState() {
	metaPath := filepath.Join(a.cfg.DataDir, "state", "meta.json")
	if b, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(b, &a.meta)
	}
	if a.meta.Protocol == "" {
		// Upgrade path from before protocol tracking: infer from the file name.
		if _, err := os.Stat(filepath.Join(a.cfg.DataDir, "config", "config.json")); err == nil {
			a.meta.Protocol = "xray"
		} else if _, err := os.Stat(filepath.Join(a.cfg.DataDir, "config", "config.yaml")); err == nil {
			a.meta.Protocol = "hysteria"
		}
	}
	name := "config.yaml"
	if a.meta.Protocol == "xray" {
		name = "config.json"
	}
	if b, err := os.ReadFile(filepath.Join(a.cfg.DataDir, "config", name)); err == nil {
		a.currentConfig = string(b)
		a.appliedRevision = a.meta.Revision
	}
}

func (a *Agent) saveLocalState() {
	dir := filepath.Join(a.cfg.DataDir, "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	b, _ := json.MarshalIndent(a.meta, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "meta.json"), b, 0o600)
}

func osName() string {
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
			}
		}
	}
	return runtime.GOOS
}

func archName() string { return runtime.GOARCH }
