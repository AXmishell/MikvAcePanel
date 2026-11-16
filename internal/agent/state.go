package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
