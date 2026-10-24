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
	"sort"
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

	runtimeMu sync.RWMutex
	runtimes  map[string]Runtime
	cores     []string

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
	currentConfigs  map[string]string
	currentRevision int64
	appliedRevision int64
	meta            nodeMeta

	certMu          sync.Mutex
	certRequestedAt time.Time
	pendingConfig   []byte
	pendingHost     string
	pendingManaged  bool

	// auth is the node-local Hysteria auth gateway and offline cache.
	auth *localAuthServer
}

type nodeMeta struct {
	Revision  int64    `json:"revision"`
	Protocol  string   `json:"protocol"`
	Cores     []string `json:"cores,omitempty"`
	TLSMode   string   `json:"tlsMode"`
	Domain    string   `json:"domain"`
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	PortRange string   `json:"portRange"`

	CertFingerprint string `json:"certFingerprint,omitempty"`
	CertNotAfter    int64  `json:"certNotAfter,omitempty"`
	KeyFingerprint  string `json:"keyFingerprint,omitempty"`
}

type wsMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// New creates an agent.
func New(cfg Config, logger *slog.Logger) (*Agent, error) {
	a := &Agent{
		cfg: cfg, log: logger,
		runtimes:       map[string]Runtime{},
		currentConfigs: map[string]string{},
		logCh:          make(chan LogLine, 2048),
		xrayOnline:     map[string]time.Time{},
		pendingLogs:    map[string]*model.LogBatch{},
		pendingStats:   map[string]*model.StatsBatch{},
	}
	a.loadLocalState()
	a.loadCertMeta()
	cores := a.meta.Cores
	if len(cores) == 0 && a.meta.Protocol != "" {
		cores = []string{a.meta.Protocol}
	}
	for _, core := range cores {
		rt, err := NewRuntime(cfg, core, a.enqueueLog)
		if err != nil {
			logger.Warn("core runtime unavailable", "core", core, "error", err)
			continue
		}
		a.runtimes[core] = rt
		a.cores = append(a.cores, core)
	}
	a.stats = newStatsPoller("", "127.0.0.1:9999")
	a.xrayStats = newXrayStatsPoller(cfg)
	if cfg.OfflineAuth {
		a.auth = newLocalAuthServer(cfg, logger, a.connectedToPanel)
	}
	return a, nil
}

// runtimeState returns the primary runtime and core for compatibility with
// single-core callers. Callers must not hold runtimeMu.
func (a *Agent) runtimeState() (Runtime, string) {
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	core := ""
	if len(a.cores) > 0 {
		core = a.cores[0]
	}
	return a.runtimes[core], core
}

// runtimesState returns a copy of the active runtime map and core list.
func (a *Agent) runtimesState() (map[string]Runtime, []string) {
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	out := make(map[string]Runtime, len(a.runtimes))
	for core, rt := range a.runtimes {
		out[core] = rt
	}
	return out, append([]string{}, a.cores...)
}

// connectedToPanel is used by the local auth gateway to avoid waiting for a
// panel timeout on every connection attempt while the control channel is down.
func (a *Agent) connectedToPanel() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connected
}

// prepareConfig applies data-directory rewrites and points Hysteria's auth URL
// at the local gateway (or back at the panel when the gateway is unavailable).
func (a *Agent) prepareConfig(text string) string {
	out := PrepareConfig(a.cfg, text)
	if a.auth != nil && a.auth.Ready() {
		return a.auth.Rewrite(out)
	}
	if base := strings.TrimRight(a.cfg.PanelURL, "/"); base != "" {
		return rewriteAuthURL(out, base, "")
	}
	return out
}

// refreshStoredConfigs updates auth URLs in the configs loaded from disk. This
// keeps the offline gateway working when AUTH_LISTEN changes between restarts,
// and restores panel auth if the gateway could not bind.
func (a *Agent) refreshStoredConfigs() {
	runtimes, cores := a.runtimesState()
	for _, core := range cores {
		rt := runtimes[core]
		if rt == nil {
			continue
		}
		a.configMu.Lock()
		text := a.currentConfigs[core]
		a.configMu.Unlock()
		if text == "" {
			continue
		}
		next := a.prepareConfig(text)
		if next == text {
			continue
		}
		var check func(string) error
		if core == "xray" {
			check = func(p string) error { return ValidateXrayConfig(a.cfg, p) }
		}
		if err := a.writeConfigFileChecked(rt, next, check); err != nil {
			a.log.Warn("refresh local config", "core", core, "error", err)
			continue
		}
		a.configMu.Lock()
		a.currentConfigs[core] = next
		if a.currentConfig == text {
			a.currentConfig = next
		}
		a.configMu.Unlock()
	}
}

func (a *Agent) hasCore(core string) bool {
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	for _, c := range a.cores {
		if c == core {
			return true
		}
	}
	return false
}

// ensureRuntimes creates runtimes for the requested cores and closes runtimes
// for cores that are no longer used.
func (a *Agent) ensureRuntimes(cores []string) error {
	a.runtimeMu.Lock()
	defer a.runtimeMu.Unlock()
	want := map[string]bool{}
	for _, core := range cores {
		core = strings.ToLower(strings.TrimSpace(core))
		if !model.ValidCore(core) {
			return fmt.Errorf("unsupported core %q", core)
		}
		want[core] = true
	}
	for core, rt := range a.runtimes {
		if !want[core] {
			rt.Close()
			delete(a.runtimes, core)
		}
	}
	active := []string{}
	for _, core := range cores {
		core = strings.ToLower(strings.TrimSpace(core))
		if !want[core] {
			continue
		}
		delete(want, core)
		if _, ok := a.runtimes[core]; !ok {
			rt, err := NewRuntime(a.cfg, core, a.enqueueLog)
			if err != nil {
				return err
			}
			a.runtimes[core] = rt
		}
		active = append(active, core)
	}
	a.cores = active
	return nil
}

func (a *Agent) ensureRuntime(core string) error {
	return a.ensureRuntimes([]string{core})
}

// Run starts the node agent and blocks until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	runtimes, cores := a.runtimesState()
	defer func() {
		for _, rt := range runtimes {
			rt.Close()
		}
	}()
	defer a.stopAccessTail()
	if a.auth != nil {
		if err := a.auth.Start(ctx); err != nil {
			a.log.Warn("local auth gateway unavailable; panel auth remains in use", "error", err)
		} else {
			a.log.Info("local auth gateway listening", "addr", a.auth.Addr())
		}
		defer a.auth.Close()
	}
	a.refreshStoredConfigs()
	go a.logBatcher(ctx)
	go a.retransmitLoop(ctx)
	go a.certRetryLoop(ctx)
	a.configMu.Lock()
	configs := make(map[string]string, len(a.currentConfigs))
	for core, text := range a.currentConfigs {
		configs[core] = text
	}
	meta := a.meta
	a.configMu.Unlock()
	for _, core := range cores {
		text := configs[core]
		rt := runtimes[core]
		if text == "" || rt == nil {
			continue
		}
		blocked := false
		if a.requiresManagedCert(core, text, meta.TLSMode) {
			host := meta.Host
			if host == "" {
				host = meta.Domain
			}
			if _, err := a.managedCertStatus(host); err != nil {
				a.log.Warn("waiting for a panel certificate before starting", "core", core, "error", err)
				blocked = true
			}
		}
		if blocked {
			continue
		}
		if core == "xray" {
			a.xrayStats.Configure(text)
			a.startAccessTail(a.accessLogPath())
		} else {
			a.stats.Configure(text)
			if meta.PortRange != "" {
				if err := SetupPortHopping(meta.Port, meta.PortRange); err != nil {
					a.log.Warn("port hopping setup", "error", err)
				}
			}
		}
		if err := rt.Start(); err != nil {
			a.log.Warn("start "+core+" from local config", "error", err)
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
	runtimes, cores := a.runtimesState()
	hy2Version, xrayVersion, runtimeName := "unknown", "", "unknown"
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
		"onlineUsers":  a.onlineCount(),
		"capabilities": caps,
		"cert": map[string]any{
			"fingerprint": certFP,
			"notAfter":    certAfter,
		},
	})
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
func (a *Agent) userSnapshotLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.write("users.request", map[string]any{})
		}
	}
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
			runtimes, cores := a.runtimesState()
			running := map[string]bool{}
			hy2Running := false
			for _, core := range cores {
				if rt := runtimes[core]; rt != nil && rt.Running() {
					running[core] = true
					if core == "hysteria" {
						hy2Running = true
					}
				}
			}
			a.write("heartbeat", map[string]any{
				"cpuPct": sys.CPUPct, "memUsed": sys.MemUsed, "memTotal": sys.MemTotal,
				"diskUsed": sys.DiskUsed, "diskTotal": sys.DiskTotal, "load1": sys.Load1,
				"uptimeSec": sys.UptimeSec, "online": a.onlineCount(),
				"trafficTx": tx, "trafficRx": rx, "hy2Running": hy2Running,
				"coresRunning": running,
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
			var batches []*model.StatsBatch
			if a.hasCore("hysteria") {
				batches = append(batches, a.stats.Poll(ctx))
			}
			if a.hasCore("xray") {
				batches = append(batches, a.xrayStats.Poll(ctx))
			}
			for _, batch := range batches {
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

// writeConfigFiles persists auxiliary TLS material pushed by the panel. ECH
// keys and the mTLS client CA are written under the shared data directory so
// both process and docker runtimes can read them at the canonical /data paths.
func (a *Agent) writeConfigFiles(files map[string]string) error {
	if len(files) == 0 {
		return nil
	}
	dir := filepath.Join(a.cfg.DataDir, "certs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create cert directory: %w", err)
	}
	for name, content := range files {
		if strings.TrimSpace(content) == "" {
			continue
		}
		var filename string
		var mode os.FileMode = 0o600
		switch name {
		case "ech":
			filename = "ech.pem"
		case "clientCA":
			filename = "client_ca.pem"
			mode = 0o644
		default:
			continue
		}
		path := filepath.Join(dir, filename)
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
			return fmt.Errorf("write %s: %w", filename, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			return fmt.Errorf("install %s: %w", filename, err)
		}
	}
	return nil
}

func (a *Agent) handleConfig(raw json.RawMessage) {
	var msg struct {
		Revision  int64             `json:"revision"`
		Protocol  string            `json:"protocol"`
		Config    string            `json:"config"`
		YAML      string            `json:"yaml"`
		Configs   map[string]string `json:"configs"`
		Cores     []string          `json:"cores"`
		TLSMode   string            `json:"tlsMode"`
		Domain    string            `json:"domain"`
		Host      string            `json:"host"`
		Port      int               `json:"port"`
		PortRange string            `json:"portRange"`
		Files     map[string]string `json:"files"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		a.log.Warn("invalid config push")
		return
	}
	configs := map[string]string{}
	for core, text := range msg.Configs {
		core = strings.ToLower(strings.TrimSpace(core))
		if core != "" && strings.TrimSpace(text) != "" {
			configs[core] = text
		}
	}
	if len(configs) == 0 {
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
		configs[protocol] = configText
	}
	cores := []string{}
	seen := map[string]bool{}
	for _, core := range msg.Cores {
		core = strings.ToLower(strings.TrimSpace(core))
		if _, ok := configs[core]; ok && !seen[core] {
			seen[core] = true
			cores = append(cores, core)
		}
	}
	if len(cores) == 0 {
		for core := range configs {
			cores = append(cores, core)
		}
		sort.Strings(cores)
	}

	if err := a.writeConfigFiles(msg.Files); err != nil {
		a.write("config.ack", map[string]any{
			"revision": msg.Revision, "ok": false, "error": err.Error(),
		})
		return
	}

	if strings.EqualFold(strings.TrimSpace(msg.TLSMode), "managed") {
		needsCert := false
		for core, text := range configs {
			if a.needsManagedCert(core, text) {
				needsCert = true
			}
		}
		if needsCert {
			host := strings.TrimSpace(msg.Host)
			if host == "" {
				host = strings.TrimSpace(msg.Domain)
			}
			if _, err := a.managedCertStatus(host); err != nil {
				a.setPendingConfig(raw, host)
				a.requestCertificate("initial", false)
				a.write("config.ack", map[string]any{
					"revision": msg.Revision, "ok": false, "pending": true,
					"error": "waiting for panel certificate: " + err.Error(),
				})
				return
			}
		}
	}
	a.clearPendingConfig()

	prepared := map[string]string{}
	for core, text := range configs {
		prepared[core] = a.prepareConfig(text)
	}
	a.configMu.Lock()
	unchanged := msg.Revision == a.appliedRevision && len(prepared) == len(a.currentConfigs)
	if unchanged {
		for core, text := range prepared {
			if a.currentConfigs[core] != text {
				unchanged = false
				break
			}
		}
	}
	previous := make(map[string]string, len(a.currentConfigs))
	for core, text := range a.currentConfigs {
		previous[core] = text
	}
	prevMeta := a.meta
	a.configMu.Unlock()
	if unchanged {
		runtimes, _ := a.runtimesState()
		running := true
		for core := range prepared {
			if rt := runtimes[core]; rt == nil || !rt.Running() {
				running = false
				break
			}
		}
		if running {
			a.write("config.ack", map[string]any{"revision": msg.Revision, "ok": true})
			return
		}
	}

	if err := a.applyConfigs(msg.Revision, cores, configs, msg.TLSMode, msg.Domain, msg.Host, msg.Port, msg.PortRange); err != nil {
		a.log.Error("apply config failed", "revision", msg.Revision, "error", err)
		// Restore the previous configuration so the node keeps serving.
		if len(previous) > 0 {
			prevCores := mapKeys(previous)
			if rerr := a.applyConfigs(prevMeta.Revision, prevCores, previous, prevMeta.TLSMode, prevMeta.Domain, prevMeta.Host, prevMeta.Port, prevMeta.PortRange); rerr != nil {
				a.log.Error("restore previous config", "error", rerr)
			}
		}
		a.write("config.ack", map[string]any{"revision": msg.Revision, "ok": false, "error": err.Error()})
		return
	}
	a.log.Info("configuration applied", "revision", msg.Revision, "cores", cores)
	a.write("config.ack", map[string]any{"revision": msg.Revision, "ok": true, "cores": cores})
}

func (a *Agent) applyConfigs(revision int64, cores []string, configs map[string]string, tlsMode, domain, host string, port int, portRange string) error {
	if len(cores) == 0 {
		return fmt.Errorf("no cores in configuration")
	}
	if err := a.ensureRuntimes(cores); err != nil {
		return err
	}
	needManaged := false
	needSelfSigned := false
	for _, core := range cores {
		text := configs[core]
		if a.requiresManagedCert(core, text, tlsMode) {
			needManaged = true
		} else if a.needsManagedCert(core, text) {
			// Hysteria manages its own ACME certificate, but Xray always reads
			// /data/certs, so a hybrid node needs the local certificate too.
			if core == "xray" || tlsMode != "acme" {
				needSelfSigned = true
			}
		}
	}
	if needManaged {
		if _, err := a.managedCertStatus(host); err != nil {
			return fmt.Errorf("managed certificate: %w", err)
		}
	} else if needSelfSigned {
		if err := EnsureSelfSignedCert(a.cfg.DataDir, host); err != nil {
			return fmt.Errorf("certificate: %w", err)
		}
	}
	runtimes, _ := a.runtimesState()
	prepared := map[string]string{}
	for _, core := range cores {
		rt := runtimes[core]
		if rt == nil {
			return fmt.Errorf("no runtime available for %s", core)
		}
		text := a.prepareConfig(configs[core])
		var check func(string) error
		if core == "xray" {
			check = func(p string) error { return ValidateXrayConfig(a.cfg, p) }
		}
		if err := a.writeConfigFileChecked(rt, text, check); err != nil {
			return err
		}
		prepared[core] = text
		if core == "xray" {
			a.xrayStats.Configure(text)
		} else {
			a.stats.Configure(text)
		}
	}
	a.configMu.Lock()
	prev := a.meta
	a.configMu.Unlock()
	if containsCore(cores, "hysteria") {
		if prev.PortRange != portRange || prev.Port != port {
			RemovePortHopping(prev.Port, prev.PortRange)
		}
		if err := SetupPortHopping(port, portRange); err != nil {
			a.log.Warn("port hopping setup failed", "error", err)
		}
	} else {
		RemovePortHopping(prev.Port, prev.PortRange)
	}
	for _, core := range cores {
		if err := runtimes[core].Restart(); err != nil {
			return fmt.Errorf("start %s: %w", core, err)
		}
	}
	// Give the processes a moment to validate their config and bind ports.
	time.Sleep(1500 * time.Millisecond)
	for _, core := range cores {
		if !runtimes[core].Running() {
			return fmt.Errorf("%s exited after applying the configuration", core)
		}
	}
	a.configMu.Lock()
	certFP, certAfter, keyFP := a.meta.CertFingerprint, a.meta.CertNotAfter, a.meta.KeyFingerprint
	if fp, after, kfp, err := a.certFileMeta(); err == nil {
		certFP, certAfter, keyFP = fp, after, kfp
	}
	a.currentConfigs = prepared
	a.currentConfig = prepared[cores[0]]
	a.currentRevision = revision
	a.appliedRevision = revision
	a.meta = nodeMeta{
		Revision: revision, Protocol: cores[0], Cores: append([]string{}, cores...),
		TLSMode: tlsMode, Domain: domain, Host: host,
		Port: port, PortRange: portRange,
		CertFingerprint: certFP, CertNotAfter: certAfter, KeyFingerprint: keyFP,
	}
	a.configMu.Unlock()
	a.saveLocalState()
	if containsCore(cores, "xray") {
		a.startAccessTail(a.accessLogPath())
	} else {
		a.stopAccessTail()
	}
	if needManaged {
		a.maybeRequestCert(context.Background(), "apply")
	}
	a.sendHello()
	return nil
}

func containsCore(cores []string, want string) bool {
	for _, core := range cores {
		if core == want {
			return true
		}
	}
	return false
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (a *Agent) writeConfigFile(rt Runtime, content string) error {
	return a.writeConfigFileChecked(rt, content, nil)
}

func (a *Agent) writeConfigFileChecked(rt Runtime, content string, check func(path string) error) error {
	if rt == nil {
		return fmt.Errorf("no runtime configured")
	}
	dir := filepath.Join(a.cfg.DataDir, "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := rt.ConfigPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	if check != nil {
		if err := check(tmp); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, path)
}

func (a *Agent) handleCommand(raw json.RawMessage) {
	var cmd struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return
	}
	var errs []string
	runtimes, cores := a.runtimesState()
	if len(runtimes) == 0 {
		a.write("command.ack", map[string]any{"id": cmd.ID, "action": cmd.Action, "ok": false, "output": "no configuration applied yet"})
		return
	}
	for _, core := range cores {
		rt := runtimes[core]
		if rt == nil {
			continue
		}
		var err error
		switch cmd.Action {
		case "start":
			err = rt.Start()
		case "stop":
			err = rt.Stop()
		case "restart":
			err = rt.Restart()
		default:
			err = fmt.Errorf("unknown action %q", cmd.Action)
		}
		if err != nil {
			errs = append(errs, core+": "+err.Error())
		}
	}
	payload := map[string]any{"id": cmd.ID, "action": cmd.Action, "ok": len(errs) == 0}
	if len(errs) > 0 {
		payload["output"] = strings.Join(errs, "; ")
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
	if a.currentConfigs == nil {
		a.currentConfigs = map[string]string{}
	}
	if b, err := os.ReadFile(filepath.Join(a.cfg.DataDir, "config", "config.yaml")); err == nil {
		a.currentConfigs["hysteria"] = string(b)
	}
	if b, err := os.ReadFile(filepath.Join(a.cfg.DataDir, "config", "config.json")); err == nil {
		a.currentConfigs["xray"] = string(b)
	}
	cores := append([]string{}, a.meta.Cores...)
	if len(cores) == 0 {
		// Upgrade path from before multi-core tracking: infer from the local
		// config files, then from the recorded primary protocol.
		for _, core := range []string{"hysteria", "xray"} {
			if _, ok := a.currentConfigs[core]; ok {
				cores = append(cores, core)
			}
		}
		if len(cores) == 0 && a.meta.Protocol != "" {
			cores = []string{a.meta.Protocol}
		}
	}
	a.meta.Cores = cores
	if len(cores) > 0 {
		if a.meta.Protocol == "" {
			a.meta.Protocol = cores[0]
		}
		a.currentConfig = a.currentConfigs[cores[0]]
	}
	a.appliedRevision = a.meta.Revision
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
