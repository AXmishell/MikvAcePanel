package model

import "time"

// UserAuthSnapshot is the last-known authentication state for one node.
//
// The panel pushes a snapshot over the authenticated agent WebSocket. The
// agent persists it and uses it only when the panel cannot answer auth
// requests. Passwords are required because Hysteria 2 authenticates clients
// with a plaintext user:password pair.
type UserAuthSnapshot struct {
	NodeID       int64      `json:"nodeId"`
	GeneratedAt  time.Time  `json:"generatedAt"`
	GraceMinutes int        `json:"graceMinutes"`
	Users        []UserAuth `json:"users"`
}

// UserAuth is one user entry in a node auth snapshot.
type UserAuth struct {
	UserID       string     `json:"userId"`
	Password     string     `json:"password"`
	Enabled      bool       `json:"enabled"`
	ExpireAt     *time.Time `json:"expireAt,omitempty"`
	TrafficTx    int64      `json:"trafficTx"`
	TrafficRx    int64      `json:"trafficRx"`
	TrafficLimit int64      `json:"trafficLimit"`
	MaxDevices   int        `json:"maxDevices"`
}

// Expired reports whether the cached account is past its expiry date.
func (u UserAuth) Expired(now time.Time) bool {
	return u.ExpireAt != nil && u.ExpireAt.Before(now)
}

// TrafficUsed returns the last-known upload + download total.
func (u UserAuth) TrafficUsed() int64 { return u.TrafficTx + u.TrafficRx }

// TrafficExceeded reports whether the cached account reached its quota.
func (u UserAuth) TrafficExceeded() bool {
	return u.TrafficLimit > 0 && u.TrafficUsed() >= u.TrafficLimit
}
