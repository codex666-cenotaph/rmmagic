#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="docker-compose.prod.yml"
ENV_FILE=".env"
BUILD=false

usage() {
  echo "Usage: $0 [--build]"
  echo "  --build   rebuild Docker images from source before deploying"
  exit 1
}

for arg in "$@"; do
  case "$arg" in
    --build) BUILD=true ;;
    --help|-h) usage ;;
    *) echo "Unknown argument: $arg"; usage ;;
  esac
done

# ── load .env ────────────────────────────────────────────────────────────────

if [[ ! -f "$ENV_FILE" ]]; then
  cat >&2 <<EOF
ERROR: $ENV_FILE not found.

Create it with the following variables:
  POSTGRES_PASSWORD=<strong-password>
  MINIO_ROOT_PASSWORD=<strong-password>
  RMM_MASTER_KEY=<64-hex-chars>   # generate: head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \\n'
  RMM_COOKIE_SECURE=true
  HTTP_PORT=80                    # optional, default 80
EOF
  exit 1
fi

# shellcheck source=.env
set -a; source "$ENV_FILE"; set +a

# ── validate required vars ───────────────────────────────────────────────────

missing=()
for var in POSTGRES_PASSWORD MINIO_ROOT_PASSWORD RMM_MASTER_KEY; do
  [[ -z "${!var:-}" ]] && missing+=("$var")
done

if [[ ${#missing[@]} -gt 0 ]]; then
  echo "ERROR: Missing required variables in $ENV_FILE: ${missing[*]}" >&2
  exit 1
fi

if [[ ${#RMM_MASTER_KEY} -ne 64 ]]; then
  echo "ERROR: RMM_MASTER_KEY must be exactly 64 hex characters (32 bytes)" >&2
  echo "       Generate one with: head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \\n'" >&2
  exit 1
fi

# ── build or pull ─────────────────────────────────────────────────────────────

if $BUILD; then
  echo "==> Building images..."
  docker compose -f "$COMPOSE_FILE" build --pull
else
  echo "==> Pulling base images..."
  docker compose -f "$COMPOSE_FILE" pull --ignore-buildable 2>/dev/null || true
fi

# ── deploy ────────────────────────────────────────────────────────────────────

echo "==> Starting services..."
docker compose -f "$COMPOSE_FILE" up -d --wait

# ── status ────────────────────────────────────────────────────────────────────

echo ""
echo "==> Services:"
docker compose -f "$COMPOSE_FILE" ps

PORT="${HTTP_PORT:-80}"
echo ""
echo "  Dashboard : http://$(hostname -I | awk '{print $1}'):${PORT}"
echo "  API       : http://$(hostname -I | awk '{print $1}'):${PORT}/api/v1/"
echo ""
echo "To bootstrap your first tenant (first deploy only):"
echo ""
echo "  docker compose -f $COMPOSE_FILE run --rm \\"
echo "    -e RMM_BOOTSTRAP_PASSWORD='your-password' \\"
echo "    server bootstrap --tenant 'My MSP' --slug my-msp --email you@example.com"
echo ""
