// Package ports defines the persistence and integration interfaces consumed by
// the application layer. Implementations live in internal/store and
// internal/server; the interfaces intentionally describe use cases instead of
// exposing the whole database schema.
package ports

import (
	"context"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// UserRepository is the persistence surface needed by the user application
// service.
type UserRepository interface {
	ListUsers(ctx context.Context, query, status string, page, size int) ([]model.User, int, error)
	GetUserByID(ctx context.Context, id int64) (*model.User, error)
	GetUserByToken(ctx context.Context, token string) (*model.User, error)
	CreateUser(ctx context.Context, u *model.User, groupIDs, nodeIDs []int64) (*model.User, error)
	UpdateUser(ctx context.Context, u *model.User, groupIDs, nodeIDs []int64) (*model.User, error)
	DeleteUser(ctx context.Context, id int64) error
	SetUserEnabled(ctx context.Context, id int64, enabled bool) error
	ResetUserSubscriptionToken(ctx context.Context, id int64) (string, error)
	SetUserSubscriptionToken(ctx context.Context, id int64, token string) error
	ResetUserPassword(ctx context.Context, id int64, password string) error
	ResetUserTraffic(ctx context.Context, id int64) error
}

// SettingsReader exposes panel settings needed by use cases and presentation.
type SettingsReader interface {
	GetSetting(ctx context.Context, key string) (string, error)
	GetSettings(ctx context.Context) (map[string]string, error)
}

// EventSink records audit events without coupling the application layer to the
// store implementation.
type EventSink interface {
	AddEvent(ctx context.Context, typ string, nodeID int64, severity, message string)
}

// DeviceTracker manages in-memory online/device state for a user.
type DeviceTracker interface {
	ResetDeviceLimit(userID string)
}

// NodeConfigSyncer asks the node subsystem to re-render and push configs after
// a change that affects Xray user lists.
type NodeConfigSyncer interface {
	SyncXrayNodes(ctx context.Context)
}

// NodeRepository is the persistence surface needed by the node application
// service.
type NodeRepository interface {
	ListNodes(ctx context.Context) ([]model.Node, error)
	GetNode(ctx context.Context, id int64) (*model.Node, error)
	CreateNode(ctx context.Context, n *model.Node, tokenHash, tokenPrefix string, groupIDs []int64) (*model.Node, error)
	UpdateNode(ctx context.Context, n *model.Node, groupIDs []int64) (*model.Node, error)
	DeleteNode(ctx context.Context, id int64) error
	ReorderNodes(ctx context.Context, ids []int64) error
	ResetNodeToken(ctx context.Context, id int64, token, hash, prefix string) error
}

// NodeConfigPusher renders, stores and optionally pushes a node configuration.
type NodeConfigPusher interface {
	SyncNodeConfig(ctx context.Context, n *model.Node, force bool) (*model.NodeConfig, bool)
	SendUserSnapshot(ctx context.Context, nodeID int64)
}

// NodeConnectionManager controls live agent connections.
type NodeConnectionManager interface {
	Disconnect(nodeID int64)
}
