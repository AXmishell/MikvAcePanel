#!/usr/bin/env bash
# Run, restart and inspect the local development stack.
#
# Starts the panel binary (bin/mikvace-server) and the Vite dev server with
# hot reload. Both are detached, so the script returns immediately. PID files
# and logs live in data/run/.
#
# Usage:
#   ./scripts/dev.sh                 # show common commands (help)
#   ./scripts/dev.sh start [--build] # build if needed, then start
#   ./scripts/dev.sh restart [--build]
#   ./scripts/dev.sh stop
#   ./scripts/dev.sh status
#   ./scripts/dev.sh logs [panel|web]
#
# Options:
#   --no-web    backend only (no Vite dev server)
#   --build     rebuild bin/mikvace-server with `make server` before starting
#   -h, --help  show this help
#
# Environment:
#   PANEL_CONFIG  config file (default: panel.yaml, created when missing)
#   PANEL_PORT    panel port for status/health checks (default: 8080)
#   WEB_PORT      Vite port (default: 5173)
#   DEV_HOST      Vite bind address (default: 0.0.0.0, LAN accessible)
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

CONFIG="${PANEL_CONFIG:-panel.yaml}"
PANEL_PORT="${PANEL_PORT:-8080}"
WEB_PORT="${WEB_PORT:-5173}"
DEV_HOST="${DEV_HOST:-0.0.0.0}"

RUN_DIR="$ROOT_DIR/data/run"
PANEL_PID_FILE="$RUN_DIR/panel.pid"
WEB_PID_FILE="$RUN_DIR/web.pid"
PANEL_LOG="$RUN_DIR/panel.log"
WEB_LOG="$RUN_DIR/web.log"

COMMAND=""
LOG_TARGET=""
WITH_WEB=1
BUILD=0

if [ -t 1 ]; then
  C_RESET=$'\033[0m'
  C_TITLE=$'\033[1;35m'
  C_SECTION=$'\033[1;36m'
  C_CMD=$'\033[1;32m'
  C_OPT=$'\033[1;33m'
  C_ENV=$'\033[1;34m'
  C_LABEL=$'\033[1;37m'
  C_DESC=$'\033[2;37m'
  C_URL=$'\033[4;36m'
  C_INFO=$'\033[36m'; C_OK=$'\033[32m'; C_WARN=$'\033[33m'; C_ERR=$'\033[31m'
else
  C_RESET=""; C_TITLE=""; C_SECTION=""; C_CMD=""; C_OPT=""; C_ENV=""
  C_LABEL=""; C_DESC=""; C_URL=""
  C_INFO=""; C_OK=""; C_WARN=""; C_ERR=""
fi

help_command() { printf '   %s%-34s%s  %s# %s%s\n' "$C_CMD" "$1" "$C_RESET" "$C_DESC" "$2" "$C_RESET"; }
help_option() { printf '   %s%-12s%s  %s%s%s\n' "$C_OPT" "$1" "$C_RESET" "$C_DESC" "$2" "$C_RESET"; }
help_env() { printf '   %s%-14s%s  %s%s%s\n' "$C_ENV" "$1" "$C_RESET" "$C_DESC" "$2" "$C_RESET"; }

usage() {
  printf '\n %sMikvAce Panel 本地开发脚本%s\n\n' "$C_TITLE" "$C_RESET"
  printf ' %s常用命令%s\n' "$C_SECTION" "$C_RESET"
  help_command "./scripts/dev.sh start" "启动（后端 + Vite 热更新，后台运行）"
  help_command "./scripts/dev.sh restart" "重启前后端"
  help_command "./scripts/dev.sh restart --build" "重新编译 bin/mikvace-server 后重启"
  help_command "./scripts/dev.sh stop" "停止"
  help_command "./scripts/dev.sh status" "查看 PID / 端口 / 健康状态"
  help_command "./scripts/dev.sh logs" "跟踪两边日志"
  help_command "./scripts/dev.sh logs panel" "只看后端"
  printf '\n %s选项%s\n' "$C_SECTION" "$C_RESET"
  help_option "--no-web" "只启动后端，不启动 Vite"
  help_option "--build" "启动/重启前执行 make server 重新编译"
  help_option "-h, --help" "显示本帮助"
  printf '\n %s环境变量%s\n' "$C_SECTION" "$C_RESET"
  help_env "PANEL_CONFIG" "配置文件（默认 panel.yaml，不存在时自动生成）"
  help_env "PANEL_PORT" "面板端口（默认 8080）"
  help_env "WEB_PORT" "Vite 端口（默认 5173）"
  help_env "DEV_HOST" "Vite 监听地址（默认 0.0.0.0，局域网可访问）"
  printf '\n %sPID 文件与日志：%sdata/run/%s\n\n' "$C_DESC" "$C_URL" "$C_RESET"
}

while [ $# -gt 0 ]; do
  case "$1" in
    start|stop|restart|status|logs) COMMAND="$1" ;;
    panel|web|all)
      [ "$COMMAND" = "logs" ] || { echo "dev: unexpected argument: $1" >&2; usage >&2; exit 2; }
      LOG_TARGET="$1"
      ;;
    --no-web) WITH_WEB=0 ;;
    --build) BUILD=1 ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "dev: unknown option: $1" >&2; usage >&2; exit 2 ;;
    *) echo "dev: unknown command: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done
COMMAND="${COMMAND:-}"
if [ -z "$COMMAND" ]; then
  usage
  exit 0
fi

log() { printf '%s==> %s%s\n' "$C_INFO" "$*" "$C_RESET"; }
ok() { printf '%s  ✓ %s%s\n' "$C_OK" "$*" "$C_RESET"; }
warn() { printf '%s  ! %s%s\n' "$C_WARN" "$*" "$C_RESET"; }
die() { printf '%s  ✗ %s%s\n' "$C_ERR" "$*" "$C_RESET" >&2; exit 1; }

detect_lan_ip() {
  ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -n1
}

LAN_IP="${LAN_IP:-$(detect_lan_ip)}"
[ -n "$LAN_IP" ] || LAN_IP="127.0.0.1"

mkdir -p "$RUN_DIR"

running() {
  local file=$1
  [ -s "$file" ] || return 1
  kill -0 "$(cat "$file")" 2>/dev/null
}

listener_pid() {
  local port=$1
  ss -ltnp "sport = :$port" 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | head -n1
}

cmd_matches() {
  local pid=$1 pattern=$2
  [ -r "/proc/$pid/cmdline" ] || return 1
  tr '\0' ' ' <"/proc/$pid/cmdline" | grep -q -- "$pattern"
}

port_owner() {
  local port=$1 pattern=$2 pid
  pid="$(listener_pid "$port")"
  [ -n "$pid" ] || return 1
  cmd_matches "$pid" "$pattern" || return 1
  printf '%s\n' "$pid"
}

wait_http() {
  local url=$1 tries=${2:-40} code
  for _ in $(seq 1 "$tries"); do
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$url" 2>/dev/null || true)"
    if [ -n "$code" ] && [ "$code" != "000" ]; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

ensure_config() {
  [ -f "$CONFIG" ] && return 0
  log "creating $CONFIG (SQLite, LAN public_url)"
  mkdir -p "$(dirname "$CONFIG")"
  cat >"$CONFIG" <<EOF
# Local development configuration generated by scripts/dev.sh.
version: 1

panel:
  name: "MikvAce Panel"
  public_url: "http://$LAN_IP:$PANEL_PORT"

server:
  addr: ":$PANEL_PORT"
  data_dir: "./data"
  serve_web: true
  frontend_url: ""
  cors_origins:
    - "http://$LAN_IP:$WEB_PORT"
    - "http://localhost:$WEB_PORT"
    - "http://127.0.0.1:$WEB_PORT"
  trusted_proxies: []
  log_level: "info"

admin:
  username: "admin"
  password_file: ""

session:
  ttl: "24h"

database:
  driver: "sqlite"
  path: "./data/panel.db"
EOF
  ok "created $CONFIG"
}

build_panel() {
  log "building bin/mikvace-server"
  make server
  ok "built bin/mikvace-server"
}

ensure_web_deps() {
  command -v npm >/dev/null 2>&1 || die "npm is required for the web dev server (use --no-web)"
  [ -x web/node_modules/.bin/vite ] || {
    log "installing web dependencies"
    ( cd web && npm ci --no-audit --no-fund )
    ok "web dependencies installed"
  }
}

start_panel() {
  if running "$PANEL_PID_FILE"; then
    ok "panel already running (pid $(cat "$PANEL_PID_FILE"))"
    return 0
  fi
  local adopted
  if adopted="$(port_owner "$PANEL_PORT" "mikvace-server")"; then
    printf '%s\n' "$adopted" >"$PANEL_PID_FILE"
    ok "panel already running (pid $adopted, adopted from port $PANEL_PORT)"
    return 0
  fi
  rm -f "$PANEL_PID_FILE"
  printf '\n===== %s starting panel =====\n' "$(date '+%F %T')" >>"$PANEL_LOG"
  (
    cd "$ROOT_DIR"
    exec ./bin/mikvace-server --config "$CONFIG"
  ) >>"$PANEL_LOG" 2>&1 &
  printf '%s\n' "$!" >"$PANEL_PID_FILE"
  if wait_http "http://127.0.0.1:$PANEL_PORT/api/health"; then
    ok "panel listening on :$PANEL_PORT (pid $(cat "$PANEL_PID_FILE"))"
  else
    warn "panel did not become healthy; last log lines:"
    tail -n 20 "$PANEL_LOG" >&2 || true
    return 1
  fi
}

start_web() {
  [ "$WITH_WEB" = "1" ] || return 0
  if running "$WEB_PID_FILE"; then
    ok "web dev server already running (pid $(cat "$WEB_PID_FILE"))"
    return 0
  fi
  local adopted
  if adopted="$(port_owner "$WEB_PORT" "vite")"; then
    printf '%s\n' "$adopted" >"$WEB_PID_FILE"
    ok "web dev server already running (pid $adopted, adopted from port $WEB_PORT)"
    return 0
  fi
  rm -f "$WEB_PID_FILE"
  printf '\n===== %s starting vite =====\n' "$(date '+%F %T')" >>"$WEB_LOG"
  (
    cd "$ROOT_DIR/web"
    exec ./node_modules/.bin/vite --host "$DEV_HOST" --port "$WEB_PORT" --strictPort
  ) >>"$WEB_LOG" 2>&1 &
  printf '%s\n' "$!" >"$WEB_PID_FILE"
  if wait_http "http://127.0.0.1:$WEB_PORT/"; then
    ok "web dev server listening on $DEV_HOST:$WEB_PORT (pid $(cat "$WEB_PID_FILE"))"
  else
    warn "web dev server did not start; last log lines:"
    tail -n 20 "$WEB_LOG" >&2 || true
    return 1
  fi
}

stop_one() {
  local name=$1 pidfile=$2 port=$3 pattern=$4 pid=""
  if [ -s "$pidfile" ]; then
    pid="$(cat "$pidfile")"
  fi
  if [ -n "$pid" ] && ! cmd_matches "$pid" "$pattern"; then
    pid=""
  fi
  if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
    pid="$(port_owner "$port" "$pattern" || true)"
  fi
  if [ -z "$pid" ]; then
    rm -f "$pidfile"
    ok "$name not running"
    return 0
  fi
  log "stopping $name (pid $pid)"
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 50); do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.2
  done
  if kill -0 "$pid" 2>/dev/null; then
    warn "$name did not exit; sending SIGKILL"
    kill -9 "$pid" 2>/dev/null || true
  fi
  rm -f "$pidfile"
  ok "$name stopped"
}

cmd_stop() {
  stop_one "web dev server" "$WEB_PID_FILE" "$WEB_PORT" "vite"
  stop_one "panel" "$PANEL_PID_FILE" "$PANEL_PORT" "mikvace-server"
}

cmd_start() {
  command -v go >/dev/null 2>&1 || die "go is required to build the panel"
  command -v curl >/dev/null 2>&1 || die "curl is required"
  ensure_config
  if [ "$BUILD" = "1" ] || [ ! -x bin/mikvace-server ]; then
    if running "$PANEL_PID_FILE"; then
      stop_one "panel" "$PANEL_PID_FILE" "$PANEL_PORT" "mikvace-server"
    fi
    build_panel
  fi
  [ "$WITH_WEB" = "1" ] && ensure_web_deps
  start_panel
  start_web
  echo
  printf '  %sPanel:%s  %shttp://%s:%s/install%s\n' "$C_LABEL" "$C_RESET" "$C_URL" "$LAN_IP" "$PANEL_PORT" "$C_RESET"
  [ "$WITH_WEB" = "1" ] && printf '  %sWeb:%s    %shttp://%s:%s/%s  %s(hot reload)%s\n' "$C_LABEL" "$C_RESET" "$C_URL" "$LAN_IP" "$WEB_PORT" "$C_RESET" "$C_DESC" "$C_RESET"
  printf '  %sLogs:%s   %s%s%s\n' "$C_LABEL" "$C_RESET" "$C_DESC" "$RUN_DIR" "$C_RESET"
}

cmd_status() {
  local panel_pid="" web_pid="" code
  if running "$PANEL_PID_FILE"; then
    panel_pid="$(cat "$PANEL_PID_FILE")"
  else
    panel_pid="$(port_owner "$PANEL_PORT" "mikvace-server" || true)"
  fi
  if running "$WEB_PID_FILE"; then
    web_pid="$(cat "$WEB_PID_FILE")"
  else
    web_pid="$(port_owner "$WEB_PORT" "vite" || true)"
  fi

  log "panel"
  if [ -n "$panel_pid" ]; then
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$PANEL_PORT/api/health" 2>/dev/null || true)"
    ok "running (pid $panel_pid), :$PANEL_PORT health=$code"
    printf '       %shttp://%s:%s/install%s\n' "$C_URL" "$LAN_IP" "$PANEL_PORT" "$C_RESET"
  else
    warn "not running"
  fi

  log "web dev server"
  if [ -n "$web_pid" ]; then
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$WEB_PORT/" 2>/dev/null || true)"
    ok "running (pid $web_pid), :$WEB_PORT http=$code"
    printf '       %shttp://%s:%s/%s  %s(hot reload)%s\n' "$C_URL" "$LAN_IP" "$WEB_PORT" "$C_RESET" "$C_DESC" "$C_RESET"
  else
    warn "not running"
  fi

  printf '\n%sLogs:%s %s%s%s\n' "$C_LABEL" "$C_RESET" "$C_DESC" "$RUN_DIR" "$C_RESET"
}

cmd_logs() {
  local which=${1:-all} files=()
  case "$which" in
    panel) [ -f "$PANEL_LOG" ] && files+=("$PANEL_LOG") ;;
    web) [ -f "$WEB_LOG" ] && files+=("$WEB_LOG") ;;
    all) [ -f "$PANEL_LOG" ] && files+=("$PANEL_LOG"); [ -f "$WEB_LOG" ] && files+=("$WEB_LOG") ;;
    *) die "logs: expected panel, web or all" ;;
  esac
  [ ${#files[@]} -gt 0 ] || die "no log files yet; start the stack first"
  exec tail -n 80 -f "${files[@]}"
}

case "$COMMAND" in
  start) cmd_start ;;
  stop) cmd_stop ;;
  restart) cmd_stop; echo; cmd_start ;;
  status) cmd_status ;;
  logs) cmd_logs "${LOG_TARGET:-all}" ;;
  *) die "unknown command: $COMMAND" ;;
esac
