#!/usr/bin/env bash
#
# redeploy.sh — resync git and redeploy the rmmagic stack via Docker Compose.
#
# Stages (each can be skipped with a flag):
#   1. sync     git fetch + hard-reset to the tracked remote branch
#   2. pull     docker compose pull (fetches the latest images from GHCR)
#   3. restart  docker compose up -d --wait (migrations run inside the container)
#
# Configuration comes from the environment, optionally via an env file
# (default: deploy/rmmagic.env — see deploy/rmmagic.env.example). Secrets
# (RMM_MASTER_KEY, POSTGRES_PASSWORD, MINIO_ROOT_PASSWORD) belong in that
# file, never in args. Set IMAGE_TAG to pin a specific release (default: latest).
#
# Usage:
#   deploy/redeploy.sh [options]
#     --branch NAME     branch to sync to        (default: current branch)
#     --env-file PATH   env file to source       (default: deploy/rmmagic.env)
#     --allow-dirty     stash local changes instead of aborting
#     --build           build images from source instead of pulling from registry
#     --no-sync         skip the git resync
#     --no-restart      skip docker compose up
#     -h, --help        show this help

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# --- defaults --------------------------------------------------------------
BRANCH=""
ENV_FILE="${ENV_FILE:-deploy/rmmagic.env}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"
ALLOW_DIRTY=0
DO_BUILD=0
DO_SYNC=1
DO_RESTART=1

# --- arg parsing -----------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --branch)      BRANCH="$2"; shift 2 ;;
    --env-file)    ENV_FILE="$2"; shift 2 ;;
    --allow-dirty) ALLOW_DIRTY=1; shift ;;
    --build)       DO_BUILD=1; shift ;;
    --no-sync)     DO_SYNC=0; shift ;;
    --no-restart)  DO_RESTART=0; shift ;;
    -h|--help)     sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

# --- helpers ---------------------------------------------------------------
log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARN\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mERROR\033[0m %s\n' "$*" >&2; exit 1; }

# --- load env file ---------------------------------------------------------
if [[ -f "$ENV_FILE" ]]; then
  log "Loading environment from $ENV_FILE"
  set -a; # shellcheck disable=SC1090
  source "$ENV_FILE"; set +a
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

# --- 2. pull or build images -----------------------------------------------
if [[ "$DO_BUILD" == 1 ]]; then
  log "Building images from source"
  docker compose -f "$COMPOSE_FILE" -f docker-compose.build.yml build --pull
else
  log "Pulling updated images (tag: ${IMAGE_TAG:-latest})"
  docker compose -f "$COMPOSE_FILE" pull
fi

# --- 3. restart ------------------------------------------------------------
if [[ "$DO_RESTART" == 1 ]]; then
  log "Restarting services"
  docker compose -f "$COMPOSE_FILE" up -d --wait
fi

log "Redeploy complete."
