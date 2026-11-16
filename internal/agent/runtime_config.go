package agent

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func PrepareConfig(cfg Config, yaml string) string {
	if cfg.Runtime == "docker" {
		return yaml
	}
	dataDir := filepath.Clean(cfg.DataDir)
	if dataDir == "" || dataDir == "/data" {
		return yaml
	}
	return strings.ReplaceAll(yaml, "/data/", strings.TrimRight(dataDir, "/")+"/")
}

// RemovePortHopping deletes previously installed redirect rules.
func RemovePortHopping(port int, ranges string) {
	if strings.TrimSpace(ranges) == "" || port <= 0 {
		return
	}
	for _, part := range strings.Split(ranges, ",") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "-") {
			continue
		}
		iptRange := strings.ReplaceAll(part, "-", ":")
		_ = exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-p", "udp", "--dport", iptRange,
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(port)).Run()
	}
}

// SetupPortHopping installs the UDP redirect rules needed by Hysteria port
// hopping. Failures are reported but do not abort configuration application.
func SetupPortHopping(port int, ranges string) error {
	ranges = strings.TrimSpace(ranges)
	if ranges == "" || port <= 0 {
		return nil
	}
	if _, err := exec.LookPath("iptables"); err != nil {
		return fmt.Errorf("iptables not available: %w", err)
	}
	var errs []string
	for _, part := range strings.Split(ranges, ",") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "-") {
			continue
		}
		iptRange := strings.ReplaceAll(part, "-", ":")
		check := exec.Command("iptables", "-t", "nat", "-C", "PREROUTING", "-p", "udp", "--dport", iptRange,
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(port))
		if err := check.Run(); err == nil {
			continue
		}
		add := exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "udp", "--dport", iptRange,
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(port))
		if out, err := add.CombinedOutput(); err != nil {
			errs = append(errs, strings.TrimSpace(string(out)))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("port hopping rules: %s", strings.Join(errs, "; "))
	}
	return nil
}
