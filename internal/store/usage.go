package store

import "context"

// DataUsage is a snapshot of the row counts that dominate database growth.
type DataUsage struct {
	Logs         int64 `json:"logs"`
	LogBatches   int64 `json:"logBatches"`
	Events       int64 `json:"events"`
	StatsBatches int64 `json:"statsBatches"`
	NodeMetrics  int64 `json:"nodeMetrics"`
}

// DataUsage counts stored telemetry rows. The queries target fixed tables so
// they stay cheap enough to run while the panel is serving requests.
func (s *Store) DataUsage(ctx context.Context) (DataUsage, error) {
	var u DataUsage
	counts := []struct {
		dst *int64
		sql string
	}{
		{&u.Logs, `SELECT COUNT(*) FROM hy2_logs`},
		{&u.LogBatches, `SELECT COUNT(*) FROM log_batches`},
		{&u.Events, `SELECT COUNT(*) FROM events`},
		{&u.StatsBatches, `SELECT COUNT(*) FROM stats_batches`},
		{&u.NodeMetrics, `SELECT COUNT(*) FROM node_metrics`},
	}
	for _, c := range counts {
		if err := s.DB.QueryRowContext(ctx, c.sql).Scan(c.dst); err != nil {
			return DataUsage{}, err
		}
	}
	return u, nil
}
