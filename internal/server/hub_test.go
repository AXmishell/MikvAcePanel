package server

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func testHub() *Hub {
	return NewHub(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHubPruneDevices(t *testing.T) {
	h := testHub()
	now := time.Now()
	h.mu.Lock()
	h.devices["idle"] = map[string]time.Time{
		"198.51.100.1": now.Add(-time.Hour),
		"198.51.100.2": now.Add(-time.Hour),
	}
	h.devices["active"] = map[string]time.Time{
		"203.0.113.1": now.Add(-time.Minute),
		"203.0.113.2": now,
	}
	h.mu.Unlock()

	h.pruneDevices(now, 15*time.Minute)

	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, ok := h.devices["idle"]; ok {
		t.Fatal("idle user entry was not removed")
	}
	active := h.devices["active"]
	if len(active) != 2 {
		t.Fatalf("active devices = %v", active)
	}
	if _, ok := active["203.0.113.1"]; !ok {
		t.Fatal("recent device removed")
	}
}

func TestHubPruneTouched(t *testing.T) {
	h := testHub()
	now := time.Now()
	h.mu.Lock()
	h.touched["stale"] = now.Add(-2 * time.Hour)
	h.touched["fresh"] = now.Add(-time.Minute)
	h.mu.Unlock()

	h.pruneTouched(now)

	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, ok := h.touched["stale"]; ok {
		t.Fatal("stale throttle entry was not removed")
	}
	if _, ok := h.touched["fresh"]; !ok {
		t.Fatal("fresh throttle entry removed")
	}
}

func TestHubDeviceGraceFallsBackWithoutStore(t *testing.T) {
	if got := testHub().deviceGrace(t.Context()); got != deviceRetentionFallback {
		t.Fatalf("grace = %v", got)
	}
}
