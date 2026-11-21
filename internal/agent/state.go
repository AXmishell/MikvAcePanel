package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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
	if cmd.Action == "restart-node" {
		// Stop cores cleanly before re-executing the agent. The new process
		// reads the persisted configs and starts them again. Keeping the stop
		// here avoids two agents fighting over the same listening ports.
		for _, core := range cores {
			rt := runtimes[core]
			if rt == nil {
				continue
			}
			if err := rt.Stop(); err != nil {
				errs = append(errs, core+": "+err.Error())
			}
			a.enqueueLog(LogLine{
				TS: time.Now().UTC(), Level: "INFO",
				Message: "agent restart requested; stopped " + core,
			})
		}
		output := "cores stopped; agent restart scheduled"
		if len(errs) > 0 {
			output += "; " + strings.Join(errs, "; ")
		}
		a.write("command.ack", map[string]any{
			"id": cmd.ID, "action": cmd.Action, "ok": len(errs) == 0,
			"output": output,
		})
		go a.restartAgent()
		return
	}
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

// restartAgent re-executes the node agent after the command acknowledgement
// and the shutdown log batch have been flushed. It intentionally keeps the
// PID, so it works inside Docker, systemd and a manually started binary
// without relying on a supervisor.
func (a *Agent) restartAgent() {
	// logBatcher flushes every two seconds; letting it run once more keeps the
	// panel's restart-loop analysis from classifying this as a crash.
	time.Sleep(2500 * time.Millisecond)
	if err := restartProcess(); err != nil {
		a.log.Error("restart node agent", "error", err)
		a.enqueueLog(LogLine{
			TS: time.Now().UTC(), Level: "ERROR",
			Message: "agent self-restart failed: " + err.Error(),
		})
		// The cores were stopped before the restart attempt. Bring them back
		// instead of leaving the node offline.
		runtimes, cores := a.runtimesState()
		for _, core := range cores {
			if rt := runtimes[core]; rt != nil {
				if startErr := rt.Start(); startErr != nil {
					a.log.Error("restart core after failed agent restart", "core", core, "error", startErr)
				}
			}
		}
	}
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
