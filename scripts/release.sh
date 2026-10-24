#!/usr/bin/env bash
# Build cross-platform release archives for a GitHub Release.
#
# Usage:
#   make release
#   VERSION=v1.2.3 COMMIT=$(git rev-parse --short HEAD) ./scripts/release.sh
#
# The panel binary embeds web/dist, so the frontend must be built first. The
# release workflow does that before invoking this script; locally use
# `make release` (which depends on the `web` target).
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${VERSION:-${GITHUB_REF_NAME:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}}"
COMMIT="${COMMIT:-${GITHUB_SHA:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}}"
COMMIT="${COMMIT:0:7}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
VERSION_NAME="${VERSION#v}"
OUT_DIR="${OUT_DIR:-$ROOT_DIR/dist}"

MODULE="github.com/axmishell/mikvacepanel"
LDFLAGS="-s -w -X ${MODULE}/internal/version.Version=${VERSION} -X ${MODULE}/internal/version.Commit=${COMMIT} -X ${MODULE}/internal/version.BuildDate=${BUILD_DATE}"

# The node agent relies on Linux syscalls, iptables and Docker, so only Linux
# builds are published. The panel and CLI are portable.
SERVER_TARGETS=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)
NODE_TARGETS=(linux/amd64 linux/arm64)
CLIENT_TARGETS=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)

log() { printf '==> %s\n' "$*"; }
fail() { printf 'release: %s\n' "$*" >&2; exit 1; }

for cmd in go tar zip; do
  command -v "$cmd" >/dev/null 2>&1 || fail "missing required command: $cmd"
done
if command -v sha256sum >/dev/null 2>&1; then
  SHA256=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  SHA256=(shasum -a 256)
else
  fail "missing required command: sha256sum or shasum"
fi
[ -f web/dist/index.html ] || fail "web/dist/index.html is missing; run 'make web' first"

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"
STAGE_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/mikvace-release.XXXXXX")"
trap 'rm -rf "$STAGE_ROOT"' EXIT

package_one() {
  local kind=$1 pkg=$2 bin=$3 example=$4 goos=$5 goarch=$6
  local ext=""
  if [[ "$goos" == "windows" ]]; then
    ext=".exe"
  fi
  local stage="$STAGE_ROOT/${kind}_${goos}_${goarch}"
  mkdir -p "$stage"

  log "build ${bin} ${goos}/${goarch}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o "$stage/${bin}${ext}" "./cmd/${pkg}"

  cp LICENSE "$stage/LICENSE"
  if [[ -n "$example" ]]; then
    cp "$example" "$stage/"
  fi

  # Smoke-test the host binary so a broken linker/ldflags set fails the release
  # before anything is uploaded.
  if [[ "$goos" == "linux" && "$goarch" == "amd64" && "$kind" == "client" ]]; then
    "$stage/${bin}${ext}" version
  fi

  local base="mikvace-${kind}_${VERSION_NAME}_${goos}_${goarch}"
  if [[ "$goos" == "windows" ]]; then
    (cd "$stage" && zip -q -X -r "$OUT_DIR/${base}.zip" .)
  else
    tar -C "$stage" -czf "$OUT_DIR/${base}.tar.gz" .
  fi
}

for target in "${SERVER_TARGETS[@]}"; do
  package_one server server mikvace-server panel.example.yaml "${target%/*}" "${target#*/}"
done
for target in "${NODE_TARGETS[@]}"; do
  package_one node node mikvace-node node.example.yaml "${target%/*}" "${target#*/}"
done
for target in "${CLIENT_TARGETS[@]}"; do
  package_one client client mikvace-client "" "${target%/*}" "${target#*/}"
done

(
  cd "$OUT_DIR"
  shopt -s nullglob
  archives=(*.tar.gz *.zip)
  ((${#archives[@]} > 0)) || fail "no release archives were created"
  "${SHA256[@]}" "${archives[@]}" > checksums.txt
)

log "release assets in $OUT_DIR"
ls -lh "$OUT_DIR"
