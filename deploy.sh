#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="docker-compose.prod.yml"
ENV_FILE=".env"
BUILD=false

usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Options:
  --build   rebuild Docker images from source before deploying
  --help    show this help

On first run this script will:
  1. Create a .env file with generated secrets (interactive)
  2. Build images
  3. Start all services
  4. Run database migrations
  5. Bootstrap the first tenant (interactive)

On subsequent runs it just brings services up (add --build to rebuild).
EOF
  exit 0
}

for arg in "$@"; do
  case "$arg" in
    --build) BUILD=true ;;
    --help|-h) usage ;;
    *) echo "Unknown argument: $arg"; usage ;;
  esac
done

# ── helpers ───────────────────────────────────────────────────────────────────

info()  { printf '\e[1;34m==>\e[0m %s\n' "$*"; }
ok()    { printf '\e[1;32m ok\e[0m %s\n' "$*"; }
die()   { printf '\e[1;31mERROR:\e[0m %s\n' "$*" >&2; exit 1; }

prompt() {
  local var="$1" label="$2" default="${3:-}"
  local value=""
  if [[ -n "$default" ]]; then
    read -rp "$label [$default]: " value
    value="${value:-$default}"
  else
    while [[ -z "$value" ]]; do
      read -rsp "$label: " value
      echo
    done
  fi
  printf -v "$var" '%s' "$value"
}

gen_key() {
  head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
}

# ── create .env if missing ────────────────────────────────────────────────────

FIRST_RUN=false

if [[ ! -f "$ENV_FILE" ]]; then
  FIRST_RUN=true
  info "No .env found — let's set one up."
  echo ""

  prompt POSTGRES_PASSWORD  "Postgres password (choose a strong one)"
  prompt MINIO_ROOT_PASSWORD "MinIO password (choose a strong one)"
  prompt HTTP_PORT          "HTTP port" "80"
  prompt RMM_COOKIE_SECURE  "Use secure cookies? (true for HTTPS, false for plain HTTP)" "false"

  GENERATED_KEY=$(gen_key)
  echo ""
  echo "  Generated RMM_MASTER_KEY: $GENERATED_KEY"
  echo "  Save this key! You need it every time the server starts."
  echo ""

  {
    echo "POSTGRES_PASSWORD=${POSTGRES_PASSWORD}"
    echo "MINIO_ROOT_PASSWORD=${MINIO_ROOT_PASSWORD}"
    echo "RMM_MASTER_KEY=${GENERATED_KEY}"
    echo "RMM_COOKIE_SECURE=${RMM_COOKIE_SECURE}"
    echo "HTTP_PORT=${HTTP_PORT}"
  } > "$ENV_FILE"
  ok ".env created"
  BUILD=true
fi

# ── load .env ────────────────────────────────────────────────────────────────

# shellcheck source=.env
set -a; source "$ENV_FILE"; set +a

# ── validate required vars ───────────────────────────────────────────────────

missing=()
for var in POSTGRES_PASSWORD MINIO_ROOT_PASSWORD RMM_MASTER_KEY; do
  [[ -z "${!var:-}" ]] && missing+=("$var")
done
[[ ${#missing[@]} -gt 0 ]] && die "Missing required variables in $ENV_FILE: ${missing[*]}"

[[ ${#RMM_MASTER_KEY} -ne 64 ]] && \
  die "RMM_MASTER_KEY must be exactly 64 hex characters. Regenerate with: head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \\n'"

# ── build or pull ─────────────────────────────────────────────────────────────

if $BUILD; then
  info "Building images..."
  docker compose -f "$COMPOSE_FILE" build --pull
else
  info "Pulling base images..."
  docker compose -f "$COMPOSE_FILE" pull --ignore-buildable 2>/dev/null || true
fi

# ── start services ────────────────────────────────────────────────────────────

info "Starting services..."
docker compose -f "$COMPOSE_FILE" up -d --wait
ok "All services healthy"

# ── bootstrap first tenant ────────────────────────────────────────────────────

if $FIRST_RUN; then
  echo ""
  info "Bootstrap first tenant"
  echo ""
  prompt BOOTSTRAP_TENANT  "Organisation name" "My MSP"
  prompt BOOTSTRAP_SLUG    "Slug (URL-safe, no spaces)" "my-msp"
  prompt BOOTSTRAP_EMAIL   "Admin email"
  prompt BOOTSTRAP_PASSWORD "Admin password (min 12 chars)"

  docker compose -f "$COMPOSE_FILE" run --rm \
    -e RMM_DATABASE_URL="postgres://rmm:${POSTGRES_PASSWORD}@postgres:5432/rmm?sslmode=disable" \
    -e RMM_BOOTSTRAP_PASSWORD="${BOOTSTRAP_PASSWORD}" \
    --entrypoint /app/rmmserver \
    server bootstrap \
      --tenant "$BOOTSTRAP_TENANT" \
      --slug "$BOOTSTRAP_SLUG" \
      --email "$BOOTSTRAP_EMAIL"

  ok "Tenant bootstrapped — you can now log in"
fi

# ── summary ───────────────────────────────────────────────────────────────────

PORT="${HTTP_PORT:-80}"
HOST=$(hostname -I | awk '{print $1}')

echo ""
docker compose -f "$COMPOSE_FILE" ps
echo ""
echo "  Dashboard : http://${HOST}:${PORT}"
echo ""
