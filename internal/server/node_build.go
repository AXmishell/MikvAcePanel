package server

import (
	"github.com/axmishell/mikvacepanel/internal/app/nodes"
	"github.com/axmishell/mikvacepanel/internal/model"
)

func (s *Server) nodeFromPayload(p *nodePayload, existing *model.Node) (*model.Node, error) {
	return nodes.Build(p.input(), existing)
}

func (s *Server) nodeResponse(n *model.Node) map[string]any {
	mode := nodeRuntime(n)
	return map[string]any{
		"node":           publicNode(n),
		"sshPasswordSet": n.SSHPassword != "",
		"sshKeySet":      n.SSHKey != "",
		"runtimeMode":    mode,
		"installCommand": s.installCommand(n, mode),
		"processCommand": s.installCommand(n, "process"),
		"dockerCommand":  s.installCommand(n, "docker"),
		"compose":        s.generatedNodeCompose(n, mode),
		"connected":      s.hub.Connected(n.ID),
	}
}

// syncNodeConfig renders and stores a node configuration, then pushes it when
// force is true or the agent is connected and has not applied it yet.
