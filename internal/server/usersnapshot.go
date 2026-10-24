package server

import (
	"context"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// userAuthSnapshot builds the offline authentication cache for one node.
//
// It contains the same users that UsersForNode already uses for Xray configs:
// enabled, not expired and below quota, filtered by the node's direct/group
// targeting rules. The effective device limit is folded into each entry so the
// agent does not need to understand groups.
func (s *Server) userAuthSnapshot(ctx context.Context, nodeID int64) (*model.UserAuthSnapshot, error) {
	users, err := s.store.UsersForNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	groupLimits, err := s.store.GroupDeviceLimits(ctx)
	if err != nil {
		return nil, err
	}
	grace := parseIntSetting(settingOrContext(s, ctx, "device_grace_minutes", "15"), 15)
	if grace <= 0 {
		grace = 15
	}
	snap := &model.UserAuthSnapshot{
		NodeID:       nodeID,
		GeneratedAt:  time.Now().UTC(),
		GraceMinutes: grace,
		Users:        make([]model.UserAuth, 0, len(users)),
	}
	for i := range users {
		u := &users[i]
		if u.UserID == "" || u.Password == "" {
			continue
		}
		limit := u.MaxDevices
		if limit == 0 {
			limit = groupLimits[u.ID]
		}
		snap.Users = append(snap.Users, model.UserAuth{
			UserID:       u.UserID,
			Password:     u.Password,
			Enabled:      u.Enabled,
			ExpireAt:     u.ExpireAt,
			TrafficTx:    u.TrafficTx,
			TrafficRx:    u.TrafficRx,
			TrafficLimit: u.TrafficLimit,
			MaxDevices:   limit,
		})
	}
	return snap, nil
}

// sendUserSnapshot pushes the current snapshot to one online agent.
func (s *Server) sendUserSnapshot(ctx context.Context, nodeID int64) bool {
	if !s.hub.Connected(nodeID) {
		return false
	}
	snap, err := s.userAuthSnapshot(ctx, nodeID)
	if err != nil {
		s.log.Warn("build user auth snapshot", "nodeId", nodeID, "error", err)
		return false
	}
	ok := s.hub.SendToNode(nodeID, "user.snapshot", snap)
	if ok {
		s.log.Debug("user auth snapshot pushed", "nodeId", nodeID, "users", len(snap.Users))
	}
	return ok
}

// pushHysteriaAuthSnapshots refreshes every online Hysteria node after a user
// or group change. Xray nodes already receive users inside their config.
func (s *Server) pushHysteriaAuthSnapshots(ctx context.Context) {
	for id := range s.hub.OnlineNodes() {
		n, err := s.store.GetNode(ctx, id)
		if err != nil || !n.HasCore("hysteria") {
			continue
		}
		s.sendUserSnapshot(ctx, id)
	}
}
