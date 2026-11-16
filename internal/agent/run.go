package agent

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func (a *Agent) Run(ctx context.Context) error {
	a.state.set(StateStarting)
	defer a.state.set(StateStopped)
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
		a.state.set(StateConnecting)
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
		a.state.set(StateBackoff)
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
