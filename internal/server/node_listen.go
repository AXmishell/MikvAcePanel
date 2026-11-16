package server

import "github.com/axmishell/mikvacepanel/internal/app/nodes"

func normalizeNodeListen(raw string, port int) (string, int, error) {
	return nodes.NormalizeNodeListen(raw, port)
}
