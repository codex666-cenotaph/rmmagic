#!/usr/bin/env bash
#
# redeploy.sh — resync git and redeploy the rmmagic build.
#
# Stages (each can be skipped with a flag):
#   1. sync     git fetch + hard-reset to the tracked remote branch
#   2. build    compile server + agent binaries, build the web dashboard
#   3. migrate  apply database migrations (golang-migrate)
#   4. restart  restart the server (systemd unit or background process)
#   5. health   poll /healthz until the server answers
#
# Configuration comes from the environment, optionally via an env file
# (default: deploy/rmmagic.env — see deploy/rmmagic.env.example). Secrets
# (RMM_MASTER_KEY, RMM_DATABASE_URL) belong in that file, never in args.
#
# Usage:
#   deploy/redeploy.sh [options]
#     --branch NAME     branch to sync to        (default: current branch)
#     --env-file PATH   env file to source       (default: deploy/rmmagic.env)
#     --allow-dirty     stash local changes instead of aborting
#     --no-sync         skip the git resync
#     --no-web          skip building the dashboard
#     --no-migrate      skip database migrations
#     --no-restart      skip restarting the server
#     --serve-web       also (re)start `vite preview` for the dashboard
#     --build-only      shorthand for --no-sync --no-migrate --no-restart
#     -h, --help        show this help

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# --- defaults --------------------------------------------------------------
BRANCH=""
ENV_FILE="${ENV_FILE:-deploy/rmmagic.env}"
ALLOW_DIRTY=0
DO_SYNC=1
DO_WEB=1
DO_MIGRATE=1
DO_RESTART=1
SERVE_WEB=0

# Runtime knobs (overridable via env / env file)
ROLES="${ROLES:-api,gateway,worker}"
RMM_LISTEN="${RMM_LISTEN:-:8080}"
SERVICE_NAME="${SERVICE_NAME:-rmmserver}"
RUN_DIR="${RUN_DIR:-$REPO_ROOT/.run}"
WEB_PORT="${WEB_PORT:-5173}"

# --- arg parsing -----------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --branch)      BRANCH="$2"; shift 2 ;;
    --env-file)    ENV_FILE="$2"; shift 2 ;;
    --allow-dirty) ALLOW_DIRTY=1; shift ;;
    --no-sync)     DO_SYNC=0; shift ;;
    --no-web)      DO_WEB=0; shift ;;
    --no-migrate)  DO_MIGRATE=0; shift ;;
    --no-restart)  DO_RESTART=0; shift ;;
    --serve-web)   SERVE_WEB=1; shift ;;
    --build-only)  DO_SYNC=0; DO_MIGRATE=0; DO_RESTART=0; shift ;;
    -h|--help)     sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

# --- helpers ---------------------------------------------------------------
log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARN\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mERROR\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# health URL derived from the listen address (host defaults to 127.0.0.1)
health_url() {
  local addr="$RMM_LISTEN" host port
  host="${addr%:*}"; port="${addr##*:}"
  [[ -z "$host" ]] && host="127.0.0.1"
  echo "http://${host}:${port}/healthz"
}

# --- load env file ---------------------------------------------------------
if [[ -f "$ENV_FILE" ]]; then
  log "Loading environment from $ENV_FILE"
  set -a; # shellcheck disable=SC1090
  source "$ENV_FILE"; set +a
  # re-apply runtime knobs that the env file may have set
  ROLES="${ROLES:-api,gateway,worker}"
  RMM_LISTEN="${RMM_LISTEN:-:8080}"
else
  warn "No env file at $ENV_FILE — relying on the current environment"
fi

# --- 1. git resync ---------------------------------------------------------
if [[ "$DO_SYNC" == 1 ]]; then
  [[ -z "$BRANCH" ]] && BRANCH="$(git rev-parse --abbrev-ref HEAD)"
  log "Syncing to origin/$BRANCH"

  if [[ -n "$(git status --porcelain)" ]]; then
    if [[ "$ALLOW_DIRTY" == 1 ]]; then
      warn "Working tree dirty — stashing local changes"
      git stash push -u -m "redeploy-autostash-$(date +%s)" >/dev/null
    else
      die "Working tree has uncommitted changes. Commit them, or pass --allow-dirty to stash."
    fi
  fi

  # Retry fetch a few times to ride out transient network failures.
  n=0
  until git fetch origin "$BRANCH"; do
    n=$((n + 1)); [[ $n -ge 4 ]] && die "git fetch failed after $n attempts"
    sleep $((2 ** n)); warn "fetch failed, retrying ($n)…"
  done
  git reset --hard "origin/$BRANCH"
  log "Now at $(git rev-parse --short HEAD) — $(git log -1 --pretty=%s)"
fi

# --- 2. build --------------------------------------------------------------
log "Building binaries"
have go || die "go toolchain not found in PATH"

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
VPKG="github.com/codex666-cenotaph/rmmagic/shared/version"
LDFLAGS="-X ${VPKG}.Version=${VERSION} -X ${VPKG}.Commit=${COMMIT}"

mkdir -p "$REPO_ROOT/bin"
( cd server && go build -ldflags "$LDFLAGS" -o "$REPO_ROOT/bin/rmmserver" ./cmd/rmmserver )
( cd agent  && CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o "$REPO_ROOT/bin/rmmagent" ./cmd/rmmagent )
log "Built bin/rmmserver and bin/rmmagent (version ${VERSION})"

if [[ "$DO_WEB" == 1 ]]; then
  have npm || die "npm not found in PATH (needed for the dashboard build)"
  log "Building dashboard"
  ( cd web
    if [[ -f package-lock.json ]]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi
    npm run build )
  log "Dashboard built to web/dist"
fi

# --- 3. migrate ------------------------------------------------------------
if [[ "$DO_MIGRATE" == 1 ]]; then
  [[ -n "${RMM_DATABASE_URL:-}" ]] || die "RMM_DATABASE_URL is required for migrations (set it in $ENV_FILE)"
  if have migrate; then
    log "Applying migrations"
    # Migrations create the schema as the privileged owner; run as the
    # connection in RMM_DATABASE_URL (not the restricted rmm_app role).
    migrate -path server/migrations -database "$RMM_DATABASE_URL" up
  else
    die "golang-migrate not installed. Install: go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest (or pass --no-migrate)"
  fi
fi

# --- 4. restart ------------------------------------------------------------
restart_systemd() {
  log "Restarting systemd service: $SERVICE_NAME"
  sudo systemctl restart "$SERVICE_NAME"
}

restart_pidfile() {
  mkdir -p "$RUN_DIR"
  local pidfile="$RUN_DIR/rmmserver.pid" logfile="$RUN_DIR/rmmserver.log"

  if [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
    log "Stopping running server (pid $(cat "$pidfile"))"
    kill -TERM "$(cat "$pidfile")" 2>/dev/null || true
    for _ in $(seq 1 10); do
      kill -0 "$(cat "$pidfile")" 2>/dev/null || break
      sleep 1
    done
    kill -KILL "$(cat "$pidfile")" 2>/dev/null || true
  fi

  [[ -n "${RMM_MASTER_KEY:-}" ]] || die "RMM_MASTER_KEY is required to run the server (set it in $ENV_FILE; keep it stable across deploys or encrypted secrets break)"
  [[ -n "${RMM_DATABASE_URL:-}" ]] || die "RMM_DATABASE_URL is required to run the server"

  log "Starting server (roles=$ROLES listen=$RMM_LISTEN)"
  nohup "$REPO_ROOT/bin/rmmserver" --roles="$ROLES" --listen="$RMM_LISTEN" \
    >>"$logfile" 2>&1 &
  echo $! >"$pidfile"
  log "Server started (pid $(cat "$pidfile")), logs at $logfile"
}

if [[ "$DO_RESTART" == 1 ]]; then
  if [[ "${RUN_MODE:-auto}" == "systemd" ]] || \
     { [[ "${RUN_MODE:-auto}" == "auto" ]] && have systemctl && systemctl list-unit-files 2>/dev/null | grep -q "^${SERVICE_NAME}\.service"; }; then
    restart_systemd
  else
    restart_pidfile
  fi
fi

# --- 5. health -------------------------------------------------------------
if [[ "$DO_RESTART" == 1 ]]; then
  url="$(health_url)"
  log "Waiting for health at $url"
  ok=0
  for _ in $(seq 1 30); do
    if curl -fsS "$url" >/dev/null 2>&1; then ok=1; break; fi
    sleep 1
  done
  [[ "$ok" == 1 ]] && log "Server healthy ✅" || die "Server did not become healthy — check logs"
fi

# --- optional: serve the dashboard ----------------------------------------
if [[ "$SERVE_WEB" == 1 ]]; then
  mkdir -p "$RUN_DIR"
  local_pid="$RUN_DIR/web.pid"
  if [[ -f "$local_pid" ]] && kill -0 "$(cat "$local_pid")" 2>/dev/null; then
    kill -TERM "$(cat "$local_pid")" 2>/dev/null || true
  fi
  log "Serving dashboard preview on port $WEB_PORT"
  ( cd web && nohup npm run preview -- --port "$WEB_PORT" --host >>"$RUN_DIR/web.log" 2>&1 & echo $! >"$local_pid" )
  log "Dashboard preview at http://127.0.0.1:${WEB_PORT} (logs at $RUN_DIR/web.log)"
fi

log "Redeploy complete."
