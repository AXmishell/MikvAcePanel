package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

	state agentState
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
	// AutoRestart is the effective panel policy for runtime crash recovery.
	// A nil value keeps the historical default (enabled).
	AutoRestart *bool `json:"autoRestart,omitempty"`
}

func (m nodeMeta) autoRestartEnabled() bool {
	return m.AutoRestart == nil || *m.AutoRestart
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
	a.state.set(StateInit)
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
		rt.SetAutoRestart(a.meta.autoRestartEnabled())
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
// keeps the offline gateway working when offline_auth.listen changes between restarts,
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

// setAutoRestart applies the effective panel recovery policy to every core.
func (a *Agent) setAutoRestart(enabled bool) {
	runtimes, _ := a.runtimesState()
	for _, rt := range runtimes {
		if rt != nil {
			rt.SetAutoRestart(enabled)
		}
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
