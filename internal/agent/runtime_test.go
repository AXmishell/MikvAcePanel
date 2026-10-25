package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessRuntimeRefreshVersion(t *testing.T) {
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "version.txt")
	if err := os.WriteFile(versionFile, []byte("v2.13.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "hysteria")
	script := "#!/bin/sh\ncat " + versionFile + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	rt := &processRuntime{protocol: "hysteria", bin: bin}
	rt.RefreshVersion()
	if got := rt.Version(); got != "v2.13.0" {
		t.Fatalf("initial version = %q", got)
	}

	// The throttle must keep a rapid heartbeat from re-running the binary.
	if err := os.WriteFile(versionFile, []byte("v2.14.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt.RefreshVersion()
	if got := rt.Version(); got != "v2.13.0" {
		t.Fatalf("throttled version = %q", got)
	}

	// After the interval the refreshed binary is reported without a restart.
	rt.mu.Lock()
	rt.versionCheckedAt = time.Now().Add(-versionRefreshInterval)
	rt.mu.Unlock()
	rt.RefreshVersion()
	if got := rt.Version(); got != "v2.14.0" {
		t.Fatalf("refreshed version = %q", got)
	}
}
