package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// nodeDataDir is the single host/container data path used by every install
// method. Keeping docker run, Compose and the generated documentation in sync
// avoids losing the offline-auth snapshot when switching methods.
const nodeDataDir = "/var/lib/mikvace"

// generatedNodeCompose renders a self-contained docker-compose.yml. The token
// is passed through the container environment, so no node.yaml/node.token
// files are required.
func (s *Server) generatedNodeCompose(n *model.Node, runtime string) string {
	panelURL := s.panelBaseURL(context.Background())
	extraEnv := ""
	extraVolume := ""
	note := ""
	if runtime == "docker" {
		extraEnv = fmt.Sprintf("\n      HOST_DATA_DIR: %q", nodeDataDir)
		extraVolume = "\n      - /var/run/docker.sock:/var/run/docker.sock"
		note = "    # Docker runtime: HOST_DATA_DIR must equal the host path mounted below.\n"
	}
	return fmt.Sprintf(`# Save as docker-compose.yml, then run: docker compose up -d
# This file contains the node token; do not commit it to a repository.
services:
  node:
    image: ghcr.io/axmishell/mikvacepanel-node:latest
    container_name: mikvace-node
    restart: unless-stopped
    # Hysteria uses UDP and port hopping edits host firewall rules.
    network_mode: host
    cap_add:
      - NET_ADMIN
%s    environment:
      PANEL_URL: %q
      NODE_TOKEN: %q
      HY2_RUNTIME: %q%s
    volumes:
      - %s:/data%s
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
`, note, panelURL, n.Token, runtime, extraEnv, nodeDataDir, extraVolume)
}

func (s *Server) panelBaseURL(ctx context.Context) string {
	pub, _ := s.store.GetSetting(ctx, "public_url")
	pub = strings.TrimRight(strings.TrimSpace(pub), "/")
	if pub == "" {
		pub = s.cfg.PublicURL
	}
	return strings.TrimRight(pub, "/")
}
