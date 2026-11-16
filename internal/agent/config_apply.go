package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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
		a.state.set(StateDegraded)
		a.write("config.ack", map[string]any{"revision": msg.Revision, "ok": false, "error": err.Error()})
		return
	}
	a.state.set(StateRunning)
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
