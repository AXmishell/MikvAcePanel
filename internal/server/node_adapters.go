package server

import (
	"context"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// serverNodeConfigPusher adapts the server's config pipeline to the node
// application service.
type serverNodeConfigPusher struct{ server *Server }

func (p serverNodeConfigPusher) SyncNodeConfig(ctx context.Context, n *model.Node, force bool) (*model.NodeConfig, bool) {
	return p.server.syncNodeConfig(ctx, n, force)
}

func (p serverNodeConfigPusher) SendUserSnapshot(ctx context.Context, nodeID int64) {
	p.server.sendUserSnapshot(ctx, nodeID)
}
