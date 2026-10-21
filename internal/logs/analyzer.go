package logs

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// Summary is the payload of the log analysis endpoint.
type Summary struct {
	From         time.Time           `json:"from"`
	To           time.Time           `json:"to"`
	GeneratedAt  time.Time           `json:"generatedAt"`
	Total        int64               `json:"total"`
	Errors       int64               `json:"errors"`
	Warnings     int64               `json:"warnings"`
	AuthSuccess  int64               `json:"authSuccess"`
	AuthFailures int64               `json:"authFailures"`
	Online       int                 `json:"online"`
	Levels       []store.EventCount  `json:"levels"`
	Events       []store.EventCount  `json:"events"`
	Timeline     []store.TimeCount   `json:"timeline"`
	TopIPs       []store.IPCount     `json:"topIps"`
	TopUsers     []store.UserCount   `json:"topUsers"`
	Nodes        []store.NodeLogStat `json:"nodes"`
	Findings     []model.Finding     `json:"findings"`
}

// Analyzer performs rule based analysis over stored Hysteria logs.
type Analyzer struct {
	Store *store.Store
}

// New creates an analyzer.
func New(st *store.Store) *Analyzer { return &Analyzer{Store: st} }

// Thresholds are configurable alert limits.
type Thresholds struct {
	AuthFailPerIP   int64
	ErrorBurstCount int64
}

// Analyze builds a summary for the requested window. online is the number of
// users currently connected according to the agent hub.
func (a *Analyzer) Analyze(ctx context.Context, opts model.LogStatsOptions, online int, th Thresholds) (*Summary, error) {
	if th.AuthFailPerIP <= 0 {
		th.AuthFailPerIP = 10
	}
	if th.ErrorBurstCount <= 0 {
		th.ErrorBurstCount = 20
	}
	if opts.To.IsZero() {
		opts.To = time.Now().UTC()
	}
	if opts.From.IsZero() {
		opts.From = opts.To.Add(-24 * time.Hour)
	}
	total, errs, warns, authOK, authFail, err := a.Store.LogCounts(ctx, opts)
	if err != nil {
		return nil, err
	}
	levels, err := a.Store.LogLevelCounts(ctx, opts)
	if err != nil {
		return nil, err
	}
	events, err := a.Store.LogEventCounts(ctx, opts)
	if err != nil {
		return nil, err
	}
	timeline, err := a.Store.LogTimeline(ctx, opts)
	if err != nil {
		return nil, err
	}
	topIPs, err := a.Store.LogTopIPs(ctx, opts, 10)
	if err != nil {
		return nil, err
	}
	topUsers, err := a.Store.LogTopUsers(ctx, opts, 10)
	if err != nil {
		return nil, err
	}
	nodeStats, err := a.Store.LogNodeStats(ctx, opts)
	if err != nil {
		return nil, err
	}
	s := &Summary{
		From: opts.From, To: opts.To, GeneratedAt: time.Now().UTC(),
		Total: total, Errors: errs, Warnings: warns, AuthSuccess: authOK, AuthFailures: authFail,
		Online: online, Levels: levels, Events: events,
		Timeline: bucketTimeline(timeline, opts.From, opts.To),
		TopIPs:   topIPs, TopUsers: topUsers, Nodes: nodeStats,
	}
	findings, err := a.findings(ctx, opts, s, th)
	if err != nil {
		return nil, err
	}
	s.Findings = findings
	return s, nil
}

func bucketTimeline(points []store.TimeCount, from, to time.Time) []store.TimeCount {
	d := to.Sub(from)
	step := 5 * time.Minute
	switch {
	case d <= 3*time.Hour:
		step = 5 * time.Minute
	case d <= 12*time.Hour:
		step = 15 * time.Minute
	case d <= 48*time.Hour:
		step = time.Hour
	case d <= 8*24*time.Hour:
		step = 6 * time.Hour
	default:
		step = 24 * time.Hour
	}
	buckets := map[int64]*store.TimeCount{}
	for _, p := range points {
		key := p.Time.UTC().Truncate(step).Unix()
		b, ok := buckets[key]
		if !ok {
			b = &store.TimeCount{Time: time.Unix(key, 0).UTC()}
			buckets[key] = b
		}
		b.Total += p.Total
		b.Errors += p.Errors
		b.AuthFailures += p.AuthFailures
	}
	start := from.UTC().Truncate(step)
	end := to.UTC().Truncate(step)
	out := []store.TimeCount{}
	for t := start; !t.After(end); t = t.Add(step) {
		if b, ok := buckets[t.Unix()]; ok {
			out = append(out, *b)
		} else {
			out = append(out, store.TimeCount{Time: t})
		}
		if len(out) > 500 {
			break
		}
	}
	return out
}

func (a *Analyzer) findings(ctx context.Context, opts model.LogStatsOptions, s *Summary, th Thresholds) ([]model.Finding, error) {
	out := []model.Finding{}
	nowT := time.Now().UTC()

	nodes, err := a.Store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	nodeName := map[int64]string{}
	for _, n := range nodes {
		nodeName[n.ID] = n.Name
		if !n.Active {
			continue
		}
		if n.Status == "error" {
			out = append(out, model.Finding{
				ID: fmt.Sprintf("node-error-%d", n.ID), Severity: "critical",
				Title: "节点配置错误", NodeID: n.ID, NodeName: n.Name,
				Description: "Agent 拒绝了最近的配置：" + n.ConfigError,
				LastSeen:    nowT, FirstSeen: nowT,
			})
		} else if n.Status != "online" {
			out = append(out, model.Finding{
				ID: fmt.Sprintf("node-offline-%d", n.ID), Severity: "warning",
				Title: "节点离线", NodeID: n.ID, NodeName: n.Name,
				Description: "节点 Agent 最近没有上报心跳。",
				LastSeen:    nowT, FirstSeen: nowT,
			})
		}
	}

	// Brute-force / credential stuffing from a single address.
	ips, err := a.Store.AuthFailuresByIP(ctx, opts, th.AuthFailPerIP)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		sev := "warning"
		if ip.Count >= th.AuthFailPerIP*3 {
			sev = "critical"
		}
		out = append(out, model.Finding{
			ID: "bruteforce-" + ip.IP, Severity: sev,
			Title: "重复认证失败", Count: ip.Count,
			Description: fmt.Sprintf("%s 在所选时间窗口内产生了 %d 次认证失败。", ip.IP, ip.Count),
			FirstSeen:   ip.LastSeen.Add(-time.Duration(ip.Count) * time.Minute), LastSeen: ip.LastSeen,
			Details: []string{"来源 IP：" + ip.IP},
		})
	}

	// Unknown user ids indicate credential stuffing or stale clients.
	unknown, err := a.Store.UnknownAuthUsers(ctx, opts, 10)
	if err != nil {
		return nil, err
	}
	var totalUnknown int64
	names := []string{}
	for _, u := range unknown {
		totalUnknown += u.Count
		if len(names) < 5 {
			names = append(names, fmt.Sprintf("%s (%d)", u.UserID, u.Count))
		}
	}
	if totalUnknown >= 5 {
		out = append(out, model.Finding{
			ID: "unknown-users", Severity: "warning",
			Title: "未知用户认证尝试", Count: totalUnknown,
			Description: "客户端尝试使用面板中不存在的用户 ID 进行认证。",
			LastSeen:    nowT, FirstSeen: nowT, Details: names,
		})
	}

	// Error bursts per node.
	bursts, err := a.Store.NodeEventCounts(ctx, "error", opts)
	if err != nil {
		return nil, err
	}
	for _, b := range bursts {
		if b.Count < th.ErrorBurstCount {
			continue
		}
		out = append(out, model.Finding{
			ID: fmt.Sprintf("error-burst-%d", b.NodeID), Severity: "warning", Count: b.Count,
			Title: "错误爆发", NodeID: b.NodeID, NodeName: b.NodeName,
			Description: fmt.Sprintf("该节点在所选窗口内记录了 %d 条错误日志。", b.Count),
			LastSeen:    nowT, FirstSeen: nowT,
		})
	}

	// Crash / restart loop.
	starts, err := a.Store.NodeEventCounts(ctx, "start", opts)
	if err != nil {
		return nil, err
	}
	for _, b := range starts {
		if b.Count < 3 {
			continue
		}
		out = append(out, model.Finding{
			ID: fmt.Sprintf("restart-loop-%d", b.NodeID), Severity: "warning", Count: b.Count,
			Title: "疑似重启循环", NodeID: b.NodeID, NodeName: b.NodeName,
			Description: "代理进程在所选时间窗口内多次启动。",
			LastSeen:    nowT, FirstSeen: nowT,
		})
	}

	// High authentication failure ratio.
	if s.AuthSuccess+s.AuthFailures >= 20 && s.AuthFailures > 0 {
		ratio := float64(s.AuthFailures) / float64(s.AuthSuccess+s.AuthFailures)
		if ratio >= 0.3 {
			out = append(out, model.Finding{
				ID: "auth-failure-ratio", Severity: "warning", Count: s.AuthFailures,
				Title:       "认证失败率过高",
				Description: fmt.Sprintf("所选窗口内 %.0f%% 的认证尝试失败。", ratio*100),
				LastSeen:    nowT, FirstSeen: nowT,
			})
		}
	}

	// TLS / ACME problems.
	for _, event := range []string{"tls", "acme"} {
		counts, err := a.Store.NodeEventCounts(ctx, event, opts)
		if err != nil {
			return nil, err
		}
		for _, c := range counts {
			if c.Count < 5 {
				continue
			}
			out = append(out, model.Finding{
				ID: fmt.Sprintf("%s-errors-%d", event, c.NodeID), Severity: "info", Count: c.Count,
				Title: strings.ToUpper(event) + " 事件", NodeID: c.NodeID, NodeName: c.NodeName,
				Description: fmt.Sprintf("记录了 %d 条 %s 相关日志。", c.Count, event),
				LastSeen:    nowT, FirstSeen: nowT,
			})
		}
	}

	// Quota warnings.
	over, err := a.Store.UsersOverQuota(ctx)
	if err != nil {
		return nil, err
	}
	if len(over) > 0 {
		details := []string{}
		for i, u := range over {
			if i >= 8 {
				break
			}
			details = append(details, fmt.Sprintf("%s (%s)", u.Username, u.UserID))
		}
		out = append(out, model.Finding{
			ID: "quota-exceeded", Severity: "warning", Count: int64(len(over)),
			Title: "用户流量达到上限", Description: "这些用户已达到流量上限，在提高配额或重置前无法继续认证。",
			FirstSeen: nowT, LastSeen: nowT, Details: details,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return severityRank(out[i].Severity) < severityRank(out[j].Severity)
	})
	_ = nodeName
	return out, nil
}

func severityRank(s string) int {
	switch s {
	case "critical":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}

// ParseRange accepts values such as "1h", "24h", "7d" or a pair of RFC3339
// timestamps separated by a comma.
func ParseRange(value string, nowT time.Time) (time.Time, time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nowT.Add(-24 * time.Hour), nowT, nil
	}
	if strings.Contains(value, ",") {
		parts := strings.SplitN(value, ",", 2)
		from, err1 := time.Parse(time.RFC3339, parts[0])
		to, err2 := time.Parse(time.RFC3339, parts[1])
		if err1 != nil || err2 != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid range timestamps")
		}
		return from.UTC(), to.UTC(), nil
	}
	if len(value) < 2 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid range")
	}
	n, err := strconv.Atoi(value[:len(value)-1])
	if err != nil || n <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid range")
	}
	var d time.Duration
	switch value[len(value)-1] {
	case 'm':
		d = time.Duration(n) * time.Minute
	case 'h':
		d = time.Duration(n) * time.Hour
	case 'd':
		d = time.Duration(n) * 24 * time.Hour
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("invalid range unit")
	}
	if d > 90*24*time.Hour {
		d = 90 * 24 * time.Hour
	}
	return nowT.Add(-d), nowT, nil
}
