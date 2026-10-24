package server

import "net/http"

const installScript = `#!/usr/bin/env bash
# MikvAce node agent installer.
# Usage:
#   curl -fsSL https://panel.example.com/api/agent/install.sh | sudo bash -s -- \
#     --panel https://panel.example.com --token mtn_xxx
set -euo pipefail

PANEL_URL=""
NODE_TOKEN=""
RUNTIME="process"
IMAGE="ghcr.io/axmishell/mikvacepanel-node:latest"
DATA_DIR="/var/lib/mikvace"
CONFIG_DIR="/etc/mikvace"
CONTAINER="mikvace-node"
INSECURE="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --panel|--panel-url) PANEL_URL="${2:-}"; shift 2 ;;
    --token) NODE_TOKEN="${2:-}"; shift 2 ;;
    --runtime) RUNTIME="${2:-process}"; shift 2 ;;
    --image) IMAGE="${2:-}"; shift 2 ;;
    --data-dir) DATA_DIR="${2:-}"; shift 2 ;;
    --config-dir) CONFIG_DIR="${2:-}"; shift 2 ;;
    --container) CONTAINER="${2:-}"; shift 2 ;;
    --insecure) INSECURE="true"; shift ;;
    -h|--help)
      echo "Options: --panel URL --token TOKEN [--runtime process|docker] [--image IMAGE] [--data-dir DIR] [--config-dir DIR] [--insecure]"
      exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$PANEL_URL" || -z "$NODE_TOKEN" ]]; then
  echo "Both --panel and --token are required." >&2
  exit 2
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required. Install it with: curl -fsSL https://get.docker.com | sh" >&2
  exit 1
fi
if [[ "$RUNTIME" != "process" && "$RUNTIME" != "docker" ]]; then
  echo "--runtime must be process or docker" >&2
  exit 2
fi
if [[ "$RUNTIME" == "docker" && "$DATA_DIR" != /* ]]; then
  echo "--data-dir must be an absolute path for the docker runtime." >&2
  exit 2
fi

echo "==> Preparing directories"
mkdir -p "$DATA_DIR" "$CONFIG_DIR"
chmod 700 "$CONFIG_DIR"
umask 077

printf '%s' "$NODE_TOKEN" > "$CONFIG_DIR/node.token"
chmod 600 "$CONFIG_DIR/node.token"

cat > "$CONFIG_DIR/node.yaml" <<EOF
version: 1
panel:
  url: "$PANEL_URL"
  token_file: "/config/node.token"
  insecure: $INSECURE
agent:
  data_dir: "/data"
  host_data_dir: "$DATA_DIR"
  log_level: "info"
runtime:
  type: "$RUNTIME"
offline_auth:
  enabled: true
  listen: "127.0.0.1:9998"
EOF
chmod 600 "$CONFIG_DIR/node.yaml"

if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  echo "==> Removing existing container $CONTAINER"
  docker rm -f "$CONTAINER" >/dev/null
fi

EXTRA=()
if [[ "$RUNTIME" == "docker" ]]; then
  if [[ ! -S /var/run/docker.sock ]]; then
    echo "Docker runtime selected but /var/run/docker.sock is missing." >&2
    exit 1
  fi
  EXTRA+=(-v /var/run/docker.sock:/var/run/docker.sock)
fi

echo "==> Starting MikvAce node agent"
docker run -d --name "$CONTAINER" --restart unless-stopped \
  --network host --cap-add NET_ADMIN \
  "${EXTRA[@]}" \
  -v "$CONFIG_DIR":/config:ro \
  -v "$DATA_DIR":/data \
  "$IMAGE" --config /config/node.yaml >/dev/null

echo "==> Done. Agent logs: docker logs -f $CONTAINER"
`

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(installScript))
}
