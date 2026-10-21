package server

import (
	"context"
	"time"
)

const (
	// quotaCheckInterval throttles the per-stats-batch quota scan.
	quotaCheckInterval = 10 * time.Second
	// quotaResyncInterval retries a reconciliation that could not push to an
	// offline agent.
	quotaResyncInterval = 5 * time.Minute
)

// ReconcileAccess applies schedule driven access changes: expired accounts are
// disabled and Xray configurations are re-rendered so expired or over-quota
// users are removed from the node.
func (s *Server) ReconcileAccess(ctx context.Context) (int64, error) {
	expired, err := s.store.ExpireUsers(ctx)
	if err != nil {
		return 0, err
	}
	if expired > 0 {
		s.SyncXrayNodes(ctx)
	}
	s.reconcileQuota(ctx)
	return expired, nil
}

// ReconcileQuota re-renders Xray configs when the over-quota user set changes.
// It is throttled because stats batches arrive every few seconds per node.
func (s *Server) ReconcileQuota(ctx context.Context) {
	if !s.beginQuotaCheck() {
		return
	}
	s.reconcileQuota(ctx)
}

func (s *Server) reconcileQuotaAsync() {
	if !s.beginQuotaCheck() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.reconcileQuota(ctx)
	}()
}

func (s *Server) beginQuotaCheck() bool {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if time.Since(s.quotaChecked) < quotaCheckInterval {
		return false
	}
	s.quotaChecked = time.Now()
	return true
}

func (s *Server) reconcileQuota(ctx context.Context) {
	over, err := s.store.UsersOverQuota(ctx)
	if err != nil {
		s.log.Warn("quota reconciliation failed", "error", err)
		return
	}
	current := make(map[string]bool, len(over))
	for i := range over {
		current[over[i].UserID] = true
	}

	s.quotaMu.Lock()
	changed := !equalUserSet(current, s.quotaBlocked)
	retry := len(current) > 0 && time.Since(s.quotaSyncedAt) > quotaResyncInterval
	if changed {
		s.quotaBlocked = current
	}
	s.quotaMu.Unlock()

	if !changed && !retry {
		return
	}
	s.SyncXrayNodes(ctx)
	s.quotaMu.Lock()
	s.quotaSyncedAt = time.Now()
	s.quotaMu.Unlock()

	if changed {
		s.log.Info("xray access reconciled", "blockedUsers", len(current))
		if len(current) > 0 {
			s.store.AddEvent(ctx, "user.quota_blocked", 0, "warning",
				"部分用户已达到流量上限，已从其 Xray 节点配置中移除")
		}
	}
}

// SyncXrayNodes re-renders and pushes every Xray node configuration.
func (s *Server) SyncXrayNodes(ctx context.Context) {
	s.syncXrayNodes(ctx)
}

func equalUserSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if !b[key] {
			return false
		}
	}
	return true
}
