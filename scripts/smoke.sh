#!/usr/bin/env bash
# MikvAce Panel smoke test: L0 build/vet, L1 tests, and panel API checks.
#
# Usage:
#   ./scripts/smoke.sh                 # L0 + L1 + temporary panel API smoke
#   ./scripts/smoke.sh --web           # also build the frontend
#   ./scripts/smoke.sh --no-api        # only L0/L1
#
# External panel:
#   SMOKE_PANEL_URL=http://127.0.0.1:8080 SMOKE_PANEL_TOKEN=... ./scripts/smoke.sh
#   SMOKE_PANEL_URL=http://127.0.0.1:8080 SMOKE_PANEL_USERNAME=admin SMOKE_PANEL_PASSWORD=... ./scripts/smoke.sh
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

WITH_WEB=0
WITH_API=1
usage() {
  cat <<'USAGE'
Usage: scripts/smoke.sh [--web] [--no-api]

  --web      also run the frontend production build (output goes to a temp dir)
  --no-api   only run L0 (build/vet) and L1 (go test)
  -h, --help show this help

Environment:
  SMOKE_PANEL_URL        use an already running panel instead of a temp one
  SMOKE_PANEL_TOKEN      admin token for the external panel
  SMOKE_PANEL_USERNAME   login username (default: admin)
  SMOKE_PANEL_PASSWORD   login password
  SMOKE_INSECURE=1       skip TLS verification for the external panel
  KEEP_TMP=1             keep the temporary data/log directory
USAGE
}
for arg in "$@"; do
  case "$arg" in
    --web) WITH_WEB=1 ;;
    --no-api) WITH_API=0 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

if [ -t 1 ]; then
  C_RESET=$'\033[0m'; C_INFO=$'\033[36m'; C_OK=$'\033[32m'; C_WARN=$'\033[33m'; C_ERR=$'\033[31m'
else
  C_RESET=""; C_INFO=""; C_OK=""; C_WARN=""; C_ERR=""
fi

step() { printf '%s==> %s%s\n' "$C_INFO" "$*" "$C_RESET"; }
ok() { printf '%s  ✓ %s%s\n' "$C_OK" "$*" "$C_RESET"; }
warn() { printf '%s  ! %s%s\n' "$C_WARN" "$*" "$C_RESET"; }
fail() { printf '%s  ✗ %s%s\n' "$C_ERR" "$*" "$C_RESET" >&2; exit 1; }

for bin in go curl jq; do
  command -v "$bin" >/dev/null 2>&1 || fail "missing required command: $bin"
done

TMP_ROOT="${TMPDIR:-/tmp}/mikvace-smoke-$$"
mkdir -p "$TMP_ROOT"
SERVER_PID=""
PANEL_URL=""
TOKEN=""
XRAY_NODE_ID=""
HYBRID_NODE_ID=""
USER_ID=""
USER_DB_ID=""
RESOURCES_CLEANED=0

cleanup_resources() {
  [ "${RESOURCES_CLEANED:-0}" = "1" ] && return 0
  [ -n "${PANEL_URL:-}" ] && [ -n "${TOKEN:-}" ] || return 0
  local curl_tls=()
  [ "${SMOKE_INSECURE:-0}" = "1" ] && curl_tls=(-k)
  if [ -n "${USER_DB_ID:-}" ]; then
    curl -sS "${curl_tls[@]}" -o /dev/null -X DELETE -H "Authorization: Bearer $TOKEN" "$PANEL_URL/api/users/$USER_DB_ID" || true
  fi
  if [ -n "${XRAY_NODE_ID:-}" ]; then
    curl -sS "${curl_tls[@]}" -o /dev/null -X DELETE -H "Authorization: Bearer $TOKEN" "$PANEL_URL/api/nodes/$XRAY_NODE_ID" || true
  fi
  if [ -n "${HYBRID_NODE_ID:-}" ]; then
    curl -sS "${curl_tls[@]}" -o /dev/null -X DELETE -H "Authorization: Bearer $TOKEN" "$PANEL_URL/api/nodes/$HYBRID_NODE_ID" || true
  fi
}

cleanup() {
  local code=$?
  cleanup_resources || true
  if [ -n "${SERVER_PID:-}" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [ "${KEEP_TMP:-0}" = "1" ]; then
    warn "temporary files kept at $TMP_ROOT"
  else
    rm -rf "$TMP_ROOT"
  fi
  exit "$code"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# L0: build, vet, optional frontend build
# ---------------------------------------------------------------------------
step "L0  go build ./..."
go build ./...
ok "build"

step "L0  go vet ./..."
go vet ./...
ok "vet"

if [ "$WITH_WEB" = "1" ]; then
  step "L0  frontend build"
  [ -d web/node_modules ] || fail "web/node_modules is missing; run (cd web && npm ci) first"
  ( cd web && npm run build -- --outDir "$TMP_ROOT/web-dist" --emptyOutDir >/dev/null )
  ok "frontend build"
else
  warn "skip frontend build (use --web)"
fi

# ---------------------------------------------------------------------------
# L1: unit and integration tests
# ---------------------------------------------------------------------------
step "L1  go test ./..."
go test -count=1 ./...
ok "tests"

if [ "$WITH_API" = "0" ]; then
  warn "skip panel API smoke (--no-api)"
  printf '\n%sL0/L1 smoke passed.%s\n' "$C_OK" "$C_RESET"
  exit 0
fi

# ---------------------------------------------------------------------------
# Panel API smoke
# ---------------------------------------------------------------------------
PANEL_URL="${SMOKE_PANEL_URL:-}"
PANEL_URL="${PANEL_URL%/}"
TOKEN="${SMOKE_PANEL_TOKEN:-}"
LOGIN_USER="${SMOKE_PANEL_USERNAME:-admin}"
LOGIN_PASS="${SMOKE_PANEL_PASSWORD:-}"
EXTERNAL=0
[ -n "$PANEL_URL" ] && EXTERNAL=1

CURL_TLS=()
[ "${SMOKE_INSECURE:-0}" = "1" ] && CURL_TLS=(-k)

if [ "$EXTERNAL" = "0" ]; then
  step "API  start temporary panel"
  command -v python3 >/dev/null 2>&1 || fail "python3 is required to pick a free port"
  PORT="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"
  PANEL_URL="http://127.0.0.1:$PORT"
  LOGIN_USER="smoke"
  LOGIN_PASS="smoke-test-password"
  mkdir -p "$TMP_ROOT/config" "$TMP_ROOT/data"
  printf '%s' "$LOGIN_PASS" > "$TMP_ROOT/config/admin.password"
  printf '%s' "smoke-$(date +%s)-$$-0123456789abcdef" > "$TMP_ROOT/config/jwt.secret"
  chmod 600 "$TMP_ROOT/config/admin.password" "$TMP_ROOT/config/jwt.secret"
  cat > "$TMP_ROOT/config/panel.yaml" <<EOF
version: 1
panel:
  name: "Smoke Panel"
  public_url: "$PANEL_URL"
server:
  addr: "127.0.0.1:$PORT"
  data_dir: "$TMP_ROOT/data"
  serve_web: false
  log_level: "warn"
admin:
  username: "$LOGIN_USER"
  password_file: "$TMP_ROOT/config/admin.password"
security:
  jwt_secret_file: "$TMP_ROOT/config/jwt.secret"
database:
  driver: sqlite
  path: "$TMP_ROOT/data/panel.db"
EOF
  go build -tags noweb -o "$TMP_ROOT/mikvace-server-api" ./cmd/server
  "$TMP_ROOT/mikvace-server-api" --config "$TMP_ROOT/config/panel.yaml" \
    >"$TMP_ROOT/server.log" 2>&1 &
  SERVER_PID=$!
  healthy=0
  for _ in $(seq 1 60); do
    if curl -fsS -m 1 "$PANEL_URL/api/health" >/dev/null 2>&1; then
      healthy=1
      break
    fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      cat "$TMP_ROOT/server.log" >&2 || true
      fail "temporary panel exited during startup"
    fi
    sleep 0.5
  done
  if [ "$healthy" != "1" ]; then
    cat "$TMP_ROOT/server.log" >&2 || true
    fail "temporary panel did not become healthy"
  fi
  ok "panel listening on $PANEL_URL"
else
  step "API  use external panel $PANEL_URL"
fi

HTTP_STATUS=""
BODY=""
call() {
  local method="$1" path="$2" data="${3:-}" auth="${4:-yes}"
  local out
  out="$(mktemp "$TMP_ROOT/resp.XXXXXX")"
  local args=(-sS "${CURL_TLS[@]}" -o "$out" -w '%{http_code}' -X "$method" "$PANEL_URL$path")
  if [ "$auth" = "yes" ]; then
    args+=(-H "Authorization: Bearer ${TOKEN:-}")
  fi
  if [ -n "$data" ]; then
    args+=(-H "Content-Type: application/json" -d "$data")
  fi
  if ! HTTP_STATUS="$(curl "${args[@]}")"; then
    rm -f "$out"
    fail "curl failed: $method $path"
  fi
  BODY="$(cat "$out")"
  rm -f "$out"
}

expect_status() {
  local want="$1" label="$2"
  if [ "$HTTP_STATUS" != "$want" ]; then
    printf '%s\n' "$BODY" >&2
    fail "$label: expected HTTP $want, got $HTTP_STATUS"
  fi
}

assert_contains() {
  case "$1" in
    *"$2"*) ;;
    *) fail "$3: missing '$2'" ;;
  esac
}

assert_not_contains() {
  case "$1" in
    *"$2"*) fail "$3: unexpected '$2'" ;;
    *) ;;
  esac
}

json_is() {
  if ! jq -e "$1" <<<"$BODY" >/dev/null 2>&1; then
    printf '%s\n' "$BODY" >&2
    fail "$2: jq check failed: $1"
  fi
}

step "API  health"
call GET /api/health "" no
expect_status 200 "health"
json_is '.ok == true' "health"
ok "health"

step "API  unauthorized request"
call GET /api/nodes "" no
expect_status 401 "unauthorized"
ok "unauthorized rejected"

if [ -n "$TOKEN" ]; then
  call GET /api/me
  if [ "$HTTP_STATUS" != "200" ]; then
    warn "provided token rejected; trying password login"
    TOKEN=""
  fi
fi

if [ -z "$TOKEN" ]; then
  [ -n "$LOGIN_PASS" ] || fail "set SMOKE_PANEL_TOKEN or SMOKE_PANEL_PASSWORD"
  step "API  login"
  LOGIN_PAYLOAD="$(jq -nc --arg u "$LOGIN_USER" --arg p "$LOGIN_PASS" '{username:$u,password:$p}')"
  call POST /api/login "$LOGIN_PAYLOAD" no
  expect_status 200 "login"
  TOKEN="$(jq -r '.token // empty' <<<"$BODY")"
  [ -n "$TOKEN" ] || fail "login returned no token"
  ok "login as $LOGIN_USER"
else
  ok "using provided token"
fi

call GET /api/me
expect_status 200 "me"
ok "/api/me"

SUFFIX="$$-$(date +%s)"

# Xray node: VLESS enabled, VMess disabled.
NODE_PAYLOAD="$(jq -nc --arg name "smoke-xray-$SUFFIX" '{
  name:$name, flag:"SM", ip:"203.0.113.90", port:443,
  cores:["xray"], tlsMode:"selfsigned", active:true,
  xray:{apiPort:61000, inbounds:[
    {protocol:"vless",tag:"vless-in",port:443,transport:"tcp",security:"reality",enabled:true},
    {protocol:"vmess",tag:"vmess-in",port:8443,transport:"ws",security:"tls",enabled:false}
  ]}
}')"
step "API  create Xray node (VLESS on, VMess off)"
call POST /api/nodes "$NODE_PAYLOAD"
expect_status 201 "create xray node"
XRAY_NODE_ID="$(jq -r '.node.id // empty' <<<"$BODY")"
[ -n "$XRAY_NODE_ID" ] || fail "create node returned no id"
ok "node $XRAY_NODE_ID"

step "API  selective protocol config"
call GET "/api/nodes/$XRAY_NODE_ID/config"
expect_status 200 "node config"
XRAY_CONFIG="$(jq -r '.configs.xray // empty' <<<"$BODY")"
[ -n "$XRAY_CONFIG" ] || fail "configs.xray is empty"
assert_contains "$XRAY_CONFIG" '"protocol": "vless"' "xray config should contain vless"
assert_not_contains "$XRAY_CONFIG" '"protocol": "vmess"' "xray config should not contain disabled vmess"
ok "only enabled protocols rendered"

step "API  private key masking"
call GET "/api/nodes/$XRAY_NODE_ID"
expect_status 200 "get node"
json_is '(.xray.realityPrivateKey // "") == ""' "legacy private key"
json_is '([.xray.inbounds[]? | .realityPrivateKey // ""] | all(. == ""))' "inbound private keys"
ok "reality private keys are not exposed"

USER_ID="smoke$SUFFIX"
USER_PAYLOAD="$(jq -nc --arg id "$USER_ID" '{userId:$id,password:"smoke-pass-123",enabled:true}')"
step "API  create user"
call POST /api/users "$USER_PAYLOAD"
expect_status 201 "create user"
SUB_TOKEN="$(jq -r '.subscriptionToken // empty' <<<"$BODY")"
USER_UUID="$(jq -r '.xrayUuid // empty' <<<"$BODY")"
USER_DB_ID="$(jq -r '.id // empty' <<<"$BODY")"
[ -n "$SUB_TOKEN" ] || fail "user has no subscription token"
[ -n "$USER_UUID" ] || fail "user has no xray uuid"
[ -n "$USER_DB_ID" ] || fail "user has no numeric id"
ok "user $USER_ID"

check_sub() {
  local format="$1" needle="$2" banned="$3"
  call GET "/api/files/$SUB_TOKEN?format=$format" "" no
  expect_status 200 "$format subscription"
  assert_contains "$BODY" "$needle" "$format subscription should contain $needle"
  if [ -n "$banned" ]; then
    assert_not_contains "$BODY" "$banned" "$format subscription should not contain $banned"
  fi
}

step "API  subscription formats"
check_sub uri "vless://$USER_UUID" "vmess://"
check_sub clash "type: vless" "type: vmess"
check_sub singbox '"type": "vless"' '"type": "vmess"'
check_sub xray-json '"protocol": "vless"' '"protocol": "vmess"'
ok "uri / clash / singbox / xray-json"

step "API  all-disabled protocols are rejected"
BAD_PAYLOAD="$(jq -nc --arg name "smoke-bad-$SUFFIX" '{
  name:$name, ip:"203.0.113.91", port:443,
  cores:["xray"], tlsMode:"selfsigned", active:true,
  xray:{inbounds:[
    {protocol:"vless",tag:"vless-in",port:443,transport:"tcp",security:"none",enabled:false}
  ]}
}')"
call POST /api/nodes "$BAD_PAYLOAD"
expect_status 400 "all-disabled node"
assert_contains "$BODY" "at least one Xray inbound" "all-disabled error"
ok "all-disabled rejected"

step "API  hybrid node renders both cores"
HYBRID_PAYLOAD="$(jq -nc --arg name "smoke-hybrid-$SUFFIX" '{
  name:$name, ip:"203.0.113.92", port:443,
  cores:["hysteria","xray"], tlsMode:"selfsigned", active:true,
  hy2:{congestion:{type:"bbr",bbrProfile:"conservative"}, quic:{maxIdleTimeout:"45s"},
       sniff:{enable:true,tcpPorts:"80,443"}, geoip:"/data/geoip.dat",
       masquerade:{type:"string",content:"hello",statusCode:200}},
  xray:{apiPort:61001, inbounds:[
    {protocol:"vless",tag:"vless-in",port:8443,transport:"tcp",security:"reality",enabled:true}
  ]}
}')"
call POST /api/nodes "$HYBRID_PAYLOAD"
expect_status 201 "create hybrid node"
HYBRID_NODE_ID="$(jq -r '.node.id // empty' <<<"$BODY")"
[ -n "$HYBRID_NODE_ID" ] || fail "hybrid node returned no id"
call GET "/api/nodes/$HYBRID_NODE_ID/config"
expect_status 200 "hybrid config"
HY2_CONFIG="$(jq -r '.configs.hysteria // empty' <<<"$BODY")"
HYBRID_XRAY="$(jq -r '.configs.xray // empty' <<<"$BODY")"
[ -n "$HY2_CONFIG" ] || fail "hybrid configs.hysteria is empty"
[ -n "$HYBRID_XRAY" ] || fail "hybrid configs.xray is empty"
assert_contains "$HYBRID_XRAY" '"protocol": "vless"' "hybrid xray config"
assert_contains "$HY2_CONFIG" "bbrProfile: conservative" "hy2 congestion"
assert_contains "$HY2_CONFIG" "maxIdleTimeout: 45s" "hy2 quic"
assert_contains "$HY2_CONFIG" "tcpPorts: 80,443" "hy2 sniff"
assert_contains "$HY2_CONFIG" "geoip: /data/geoip.dat" "hy2 geo database"
assert_contains "$HY2_CONFIG" "type: string" "hy2 masquerade"
ok "hybrid node $HYBRID_NODE_ID"

step "API  cleanup"
call DELETE "/api/users/$USER_DB_ID"
call DELETE "/api/nodes/$XRAY_NODE_ID"
call DELETE "/api/nodes/$HYBRID_NODE_ID"
RESOURCES_CLEANED=1
ok "cleanup"

printf '\n%sAll smoke checks passed.%s\n' "$C_OK" "$C_RESET"
