package server

import "context"

// serverNodeSyncer adapts Server's config push loop to the users application
// service without exposing the whole Server to the application layer.
type serverNodeSyncer struct{ server *Server }

func (s serverNodeSyncer) SyncXrayNodes(ctx context.Context) { s.server.syncXrayNodes(ctx) }
