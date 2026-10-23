package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// ---- Xray access log tail ------------------------------------------------

func (a *Agent) accessLogPath() string {
	return filepath.Join(a.cfg.DataDir, "log", "access.log")
}

func (a *Agent) startAccessTail(path string) {
	a.stopAccessTail()
	ctx, cancel := context.WithCancel(context.Background())
	a.tailMu.Lock()
	a.tailStop = cancel
	a.tailMu.Unlock()
	go tailFile(ctx, path, a.enqueueLog)
}

func (a *Agent) stopAccessTail() {
	a.tailMu.Lock()
	cancel := a.tailStop
	a.tailStop = nil
	a.tailMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func tailFile(ctx context.Context, path string, sink func(LogLine)) {
	var offset int64 = -1
	var partial []byte
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		stat, err := f.Stat()
		if err != nil {
			f.Close()
			continue
		}
		if offset < 0 {
			// Start at the end of a pre-existing log; new sessions are what we
			// care about after an agent restart.
			offset = stat.Size()
			f.Close()
			continue
		}
		if stat.Size() < offset {
			offset = 0
			partial = nil
		}
		if stat.Size() == offset {
			f.Close()
			continue
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, stat.Size()-offset))
		f.Close()
		if err != nil || len(data) == 0 {
			continue
		}
		offset += int64(len(data))
		data = append(partial, data...)
		cut := strings.LastIndexByte(string(data), '\n')
		if cut < 0 {
			partial = data
			continue
		}
		complete := data[:cut]
		partial = append([]byte{}, data[cut+1:]...)
		offset -= int64(len(partial))
		sc := bufio.NewScanner(strings.NewReader(string(complete)))
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			if sink != nil {
				sink(LogLine{TS: time.Now().UTC(), Level: "INFO", Message: line, Raw: line})
			}
		}
	}
}

// ---- Xray stats API via the xray CLI --------------------------------------

type xrayStatsPoller struct {
	bin       string
	docker    bool
	container string
	mu        sync.RWMutex
	server    string
	totalTx   atomic.Int64
	totalRx   atomic.Int64
}

func newXrayStatsPoller(cfg Config) *xrayStatsPoller {
	return &xrayStatsPoller{
		bin: cfg.XrayBin, docker: cfg.Runtime == "docker",
		container: containerName(cfg, "xray"),
	}
}

func (x *xrayStatsPoller) command(ctx context.Context, args ...string) *exec.Cmd {
	if x.docker {
		full := append([]string{"exec", x.container, x.bin}, args...)
		return exec.CommandContext(ctx, "docker", full...)
	}
	return exec.CommandContext(ctx, x.bin, args...)
}

// ValidateXrayConfig runs `xray run -test` so a broken config never replaces a
// working process.
func ValidateXrayConfig(cfg Config, configPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if cfg.Runtime == "docker" {
		containerPath := "/data/" + strings.TrimPrefix(configPath, filepath.Clean(cfg.DataDir)+string(os.PathSeparator))
		cmd = exec.CommandContext(ctx, "docker", "run", "--rm",
			"-v", cfg.HostDataDir+":/data",
			cfg.XrayImage, "run", "-test", "-c", containerPath)
	} else {
		cmd = exec.CommandContext(ctx, cfg.XrayBin, "run", "-test", "-c", configPath)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray config test failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (x *xrayStatsPoller) Configure(configText string) {
	var cfg struct {
		Inbounds []struct {
			Protocol string `json:"protocol"`
			Port     int    `json:"port"`
			Tag      string `json:"tag"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(configText), &cfg); err != nil {
		return
	}
	port := 0
	for _, in := range cfg.Inbounds {
		if in.Protocol == "dokodemo-door" && in.Port > 0 {
			port = in.Port
			break
		}
	}
	if port == 0 {
		return
	}
	x.mu.Lock()
	x.server = "127.0.0.1:" + strconv.Itoa(port)
	x.mu.Unlock()
}

func (x *xrayStatsPoller) Poll(ctx context.Context) *model.StatsBatch {
	x.mu.RLock()
	server := x.server
	x.mu.RUnlock()
	if server == "" {
		return nil
	}
	cmd := x.command(ctx, "api", "statsquery", "--server="+server, "-pattern=", "-reset")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var result struct {
		Stat []struct {
			Name  string      `json:"name"`
			Value json.Number `json:"value"`
		} `json:"stat"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil
	}
	users := map[string]model.UserTraffic{}
	var sumTx, sumRx int64
	for _, stat := range result.Stat {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		uid := parts[1]
		value, _ := stat.Value.Int64()
		if value == 0 {
			continue
		}
		ut := users[uid]
		switch parts[3] {
		case "uplink":
			ut.Tx += value
			sumTx += value
		case "downlink":
			ut.Rx += value
			sumRx += value
		}
		users[uid] = ut
	}
	x.totalTx.Add(sumTx)
	x.totalRx.Add(sumRx)
	if len(users) == 0 {
		return nil
	}
	return &model.StatsBatch{
		BatchID: randomID(), Users: users,
		TrafficTx: x.totalTx.Load(), TrafficRx: x.totalRx.Load(),
	}
}

func (x *xrayStatsPoller) Totals() (int64, int64) { return x.totalTx.Load(), x.totalRx.Load() }

var _ = fmt.Sprint
