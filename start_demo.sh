#!/usr/bin/env bash
# start_demo.sh — one-shot launcher for the devkit demo.
#
#   ./start_demo.sh              build + start bffsim :4000, UI :3001, 15 bots
#   ./start_demo.sh --no-bots    skip bot-runner
#   ./start_demo.sh --dev        use `npm run dev` instead of prod build (hot reload, slower)
#   ./start_demo.sh stop         stop everything started by this script
#   ./start_demo.sh logs         tail all three logs together
#
# State:
#   .devkit/pids     — PIDs of launched processes
#   .devkit/*.log    — stdout+stderr of each process

set -eu

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

STATE_DIR="$ROOT/.devkit"
PID_FILE="$STATE_DIR/pids"
BFFSIM_LOG="$STATE_DIR/bffsim.log"
BOTS_LOG="$STATE_DIR/bots.log"
UI_LOG="$STATE_DIR/ui.log"

BFFSIM_PORT=4000
UI_PORT=3001

mkdir -p "$STATE_DIR"

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

log()  { printf '\033[1;36m[devkit]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[devkit]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[devkit]\033[0m %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"; }

port_in_use() {
  if command -v ss >/dev/null 2>&1; then
    ss -ltn "( sport = :$1 )" 2>/dev/null | tail -n +2 | grep -q .
  else
    lsof -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
  fi
}

free_port() {
  local port=$1
  if port_in_use "$port"; then
    warn "port :$port in use — killing occupants"
    fuser -k "$port/tcp" 2>/dev/null || true
    sleep 1
  fi
}

record_pid() { printf '%s\n' "$1" >> "$PID_FILE"; }

# ---------------------------------------------------------------------------
# sub-commands
# ---------------------------------------------------------------------------

cmd_stop() {
  if [ ! -f "$PID_FILE" ]; then
    log "nothing to stop (no $PID_FILE)"
    return 0
  fi
  while read -r pid; do
    [ -z "$pid" ] && continue
    if kill -0 "$pid" 2>/dev/null; then
      log "stopping PID $pid"
      kill "$pid" 2>/dev/null || true
    fi
  done < "$PID_FILE"
  sleep 1
  # Fall back to port kill for stragglers
  free_port "$BFFSIM_PORT"
  free_port "$UI_PORT"
  rm -f "$PID_FILE"
  log "stopped."
}

cmd_logs() {
  [ -f "$BFFSIM_LOG" ] || die "no logs yet — start the demo first"
  exec tail -F "$BFFSIM_LOG" "$BOTS_LOG" "$UI_LOG"
}

# ---------------------------------------------------------------------------
# parse args
# ---------------------------------------------------------------------------

MODE="start"
WITH_BOTS=1
UI_DEV_MODE=0

while [ $# -gt 0 ]; do
  case "$1" in
    stop)       MODE="stop" ;;
    logs)      MODE="logs" ;;
    --no-bots) WITH_BOTS=0 ;;
    --dev)     UI_DEV_MODE=1 ;;
    -h|--help)
      sed -n '2,11p' "$0"
      exit 0
      ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
  shift
done

case "$MODE" in
  stop) cmd_stop; exit 0 ;;
  logs) cmd_logs; exit 0 ;;
esac

# ---------------------------------------------------------------------------
# preflight
# ---------------------------------------------------------------------------

need go
need node
need npm

# Clean previous run
if [ -f "$PID_FILE" ]; then
  warn "previous run detected — stopping first"
  cmd_stop
fi

free_port "$BFFSIM_PORT"
free_port "$UI_PORT"

: > "$PID_FILE"
: > "$BFFSIM_LOG"
: > "$BOTS_LOG"
: > "$UI_LOG"

# ---------------------------------------------------------------------------
# build
# ---------------------------------------------------------------------------

log "compiling Go binaries (bffsim, bot-runner)…"
go build -o "$STATE_DIR/bffsim"     ./cmd/bffsim
go build -o "$STATE_DIR/bot-runner" ./cmd/bot-runner

if [ "$UI_DEV_MODE" -eq 0 ]; then
  if [ ! -d ui/node_modules ]; then
    log "installing UI deps (first run)…"
    (cd ui && npm install --silent)
  fi
  if [ ! -d ui/.next ]; then
    log "building UI (Next prod bundle)…"
    (cd ui && npm run build)
  else
    log "UI build cache found — skipping rebuild (delete ui/.next to force)"
  fi
fi

# ---------------------------------------------------------------------------
# launch
# ---------------------------------------------------------------------------

log "starting bffsim on :$BFFSIM_PORT"
( "$STATE_DIR/bffsim" >"$BFFSIM_LOG" 2>&1 ) &
record_pid $!

# Wait until bffsim is ready
for _ in $(seq 1 20); do
  if curl -fsS "http://localhost:$BFFSIM_PORT/health" >/dev/null 2>&1; then break; fi
  sleep 0.25
done
curl -fsS "http://localhost:$BFFSIM_PORT/health" >/dev/null || die "bffsim failed to come up — see $BFFSIM_LOG"

log "starting UI on :$UI_PORT"
if [ "$UI_DEV_MODE" -eq 1 ]; then
  ( cd ui && exec npm run dev -- --port "$UI_PORT" >"$UI_LOG" 2>&1 ) &
else
  ( cd ui && exec npm start -- --port "$UI_PORT" >"$UI_LOG" 2>&1 ) &
fi
record_pid $!

if [ "$WITH_BOTS" -eq 1 ]; then
  log "starting bot-runner (15 bots)"
  ( "$STATE_DIR/bot-runner" >"$BOTS_LOG" 2>&1 ) &
  record_pid $!
fi

sleep 2

# ---------------------------------------------------------------------------
# summary
# ---------------------------------------------------------------------------

cat <<EOF

  ┌─ ExoHash DevKit demo ────────────────────────────────────────┐
  │                                                              │
  │  UI     http://localhost:$UI_PORT                                  │
  │  BFF    http://localhost:$BFFSIM_PORT                                  │
  │  SSE    http://localhost:$BFFSIM_PORT/stream                           │
  │                                                              │
  │  Logs   .devkit/{bffsim,ui,bots}.log                         │
  │         ./start_demo.sh logs       tail all three            │
  │         ./start_demo.sh stop       shut everything down      │
  │                                                              │
  └──────────────────────────────────────────────────────────────┘

EOF

log "running — Ctrl-C to stop."

cleanup() { log "shutting down…"; cmd_stop; exit 0; }
trap cleanup INT TERM

# Follow bffsim in the foreground (most interesting log)
tail -F "$BFFSIM_LOG"
