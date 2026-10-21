package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/axmishell/mikvacepanel/internal/model"
)

type localHysteriaConfig struct {
	Listen       string `yaml:"listen"`
	TrafficStats struct {
		Listen string `yaml:"listen"`
		Secret string `yaml:"secret"`
	} `yaml:"trafficStats"`
}

type statsPoller struct {
	client   *http.Client
	secret   string
	baseURL  string
	totalTx  atomic.Int64
	totalRy  atomic.Int64
	onlineMu sync.RWMutex
	online   map[string]int
}

func newStatsPoller(secret, listen string) *statsPoller {
	addr := strings.TrimSpace(listen)
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	if addr == "" {
		addr = "127.0.0.1:9999"
	}
	return &statsPoller{
		client: &http.Client{
			Timeout:   8 * time.Second,
			Transport: &http.Transport{Proxy: nil},
		},
		secret: secret, baseURL: "http://" + addr,
		online: map[string]int{},
	}
}

// Configure updates the stats endpoint from a freshly applied YAML config.
func (s *statsPoller) Configure(yamlText string) {
	var c localHysteriaConfig
	if err := yaml.Unmarshal([]byte(yamlText), &c); err != nil {
		return
	}
	if c.TrafficStats.Listen != "" {
		addr := c.TrafficStats.Listen
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		}
		s.baseURL = "http://" + addr
	}
	s.secret = c.TrafficStats.Secret
}

func (s *statsPoller) request(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if s.secret != "" {
		req.Header.Set("Authorization", s.secret)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stats API returned %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// Poll returns a traffic batch or nil when the stats API is unreachable.
func (s *statsPoller) Poll(ctx context.Context) *model.StatsBatch {
	body, err := s.request(ctx, "/traffic?clear=1")
	if err != nil {
		return nil
	}
	var raw map[string]struct {
		Tx uint64 `json:"tx"`
		Rx uint64 `json:"rx"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	users := map[string]model.UserTraffic{}
	var sumTx, sumRx int64
	for uid, v := range raw {
		d := model.UserTraffic{Tx: int64(v.Tx), Rx: int64(v.Rx)}
		if d.Tx == 0 && d.Rx == 0 {
			continue
		}
		users[uid] = d
		sumTx += d.Tx
		sumRx += d.Rx
	}
	online := map[string]int{}
	if b, err := s.request(ctx, "/online"); err == nil {
		_ = json.Unmarshal(b, &online)
	}
	s.totalTx.Add(sumTx)
	s.totalRy.Add(sumRx)
	s.onlineMu.Lock()
	s.online = online
	s.onlineMu.Unlock()
	return &model.StatsBatch{
		BatchID: randomID(), Users: users, Online: online,
		TrafficTx: s.totalTx.Load(), TrafficRx: s.totalRy.Load(),
	}
}

// Totals returns the cumulative counters since agent start.
func (s *statsPoller) Totals() (int64, int64) { return s.totalTx.Load(), s.totalRy.Load() }

// OnlineCount returns the live user count from the last poll.
func (s *statsPoller) OnlineCount() int {
	s.onlineMu.RLock()
	defer s.onlineMu.RUnlock()
	n := 0
	for _, v := range s.online {
		n += v
	}
	return n
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
