package agent

import (
	"context"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

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
			desired := map[string]bool{}
			hy2Running := false
			for _, core := range cores {
				rt := runtimes[core]
				if rt == nil {
					continue
				}
				desired[core] = rt.DesiredRunning()
				if rt.Running() {
					running[core] = true
					if core == "hysteria" {
						hy2Running = true
					}
				}
			}
			hy2Version, xrayVersion, runtimeName, _ := a.snapshotVersions(true)
			a.write("heartbeat", map[string]any{
				"cpuPct": sys.CPUPct, "memUsed": sys.MemUsed, "memTotal": sys.MemTotal,
				"diskUsed": sys.DiskUsed, "diskTotal": sys.DiskTotal, "load1": sys.Load1,
				"uptimeSec": sys.UptimeSec, "online": a.onlineCount(),
				"trafficTx": tx, "trafficRx": rx, "hy2Running": hy2Running,
				"coresRunning": running, "coresDesired": desired,
				"version":     a.cfg.Version,
				"hy2Version":  hy2Version,
				"xrayVersion": xrayVersion,
				"runtime":     runtimeName,
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
