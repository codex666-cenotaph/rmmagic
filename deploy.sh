#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="docker-compose.prod.yml"
ENV_FILE=".env"
BUILD=false
BOOTSTRAP=false

usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Options:
  --build       rebuild Docker images from source before deploying
  --bootstrap   re-run tenant bootstrap (use when database was wiped)
  --help        show this help

On first run this script will:
  1. Create a .env file with generated secrets (interactive)
  2. Build images
  3. Start all services
  4. Run database migrations
  5. Bootstrap the first tenant (interactive)

On subsequent runs it just brings services up (add --build to rebuild).
Use --bootstrap to create a new tenant after wiping the database.
EOF
  exit 0
}

for arg in "$@"; do
  case "$arg" in
    --build)     BUILD=true ;;
    --bootstrap) BOOTSTRAP=true ;;
    --help|-h)   usage ;;
    *) echo "Unknown argument: $arg"; usage ;;
  esac
done

# ── helpers ───────────────────────────────────────────────────────────────────

info()  { printf '\e[1;34m==>\e[0m %s\n' "$*"; }
ok()    { printf '\e[1;32m ok\e[0m %s\n' "$*"; }
die()   { printf '\e[1;31mERROR:\e[0m %s\n' "$*" >&2; exit 1; }

prompt() {
  local var="$1" label="$2" default="${3:-}" secret="${4:-false}"
  local value=""
  if [[ -n "$default" ]]; then
    read -rp "$label [$default]: " value
    value="${value:-$default}"
  elif [[ "$secret" == "true" ]]; then
    while [[ -z "$value" ]]; do
      read -rsp "$label: " value
      echo
    done
  else
    while [[ -z "$value" ]]; do
      read -rp "$label: " value
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

  GENERATED_PG_PASS=$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')
  GENERATED_MINIO_PASS=$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')
  echo "  Generated Postgres password : $GENERATED_PG_PASS"
  echo "  Generated MinIO password    : $GENERATED_MINIO_PASS"
  echo "  (hex-only passwords avoid URL encoding issues)"
  echo ""
  prompt POSTGRES_PASSWORD   "Postgres password" "$GENERATED_PG_PASS"
  prompt MINIO_ROOT_PASSWORD "MinIO password"    "$GENERATED_MINIO_PASS"
  prompt HTTP_PORT          "HTTP port" "80"
  prompt RMM_COOKIE_SECURE  "Use secure cookies? (true for HTTPS, false for plain HTTP)" "false"

  GENERATED_KEY=$(gen_key)
  echo ""
  echo "  Generated RMM_MASTER_KEY: $GENERATED_KEY"
  echo "  Save this key! You need it every time the server starts."
  echo ""

  {
    printf "POSTGRES_PASSWORD='%s'\n"    "${POSTGRES_PASSWORD}"
    printf "MINIO_ROOT_PASSWORD='%s'\n"  "${MINIO_ROOT_PASSWORD}"
    printf "RMM_MASTER_KEY='%s'\n"       "${GENERATED_KEY}"
    printf "RMM_COOKIE_SECURE='%s'\n"    "${RMM_COOKIE_SECURE}"
    printf "HTTP_PORT='%s'\n"            "${HTTP_PORT}"
  } > "$ENV_FILE"
  ok ".env created"
  BUILD=true

  # Guard against a stale Postgres volume from an earlier deploy. POSTGRES_PASSWORD
  # only takes effect when the data directory is first initialised; a pre-existing
  # volume keeps its original password, so the freshly generated credentials above
  # won't match and migrations fail with "password authentication failed for user
  # rmm". Since we just created new credentials, any existing volume is stale.
  PROJECT="${COMPOSE_PROJECT_NAME:-$(basename "$PWD" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9_-')}"
  PG_VOLUME="${PROJECT}_pgdata"
  if docker volume inspect "$PG_VOLUME" >/dev/null 2>&1; then
    echo ""
    info "Found an existing Postgres volume '$PG_VOLUME' from a previous deploy."
    echo "  Its password won't match the credentials just generated, so the server"
    echo "  would fail to start (password authentication failed)."
    echo ""
    prompt WIPE_VOLUME "Remove the old volume and start clean? (destroys existing DB data)" "yes"
    if [[ "$WIPE_VOLUME" =~ ^[Yy] ]]; then
      docker compose -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
      ok "Old volumes removed — Postgres will re-initialise with the new password"
    else
      echo ""
      echo "  Keeping it. If the server fails to start, reset the password to match"
      echo "  .env, or re-run after: docker compose -f $COMPOSE_FILE down -v"
      echo ""
    fi
  fi
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
  info "Building images from source..."
  docker compose -f "$COMPOSE_FILE" -f docker-compose.build.yml build --pull
else
  info "Pulling pre-built images from registry..."
  docker compose -f "$COMPOSE_FILE" pull
fi

# ── start services ────────────────────────────────────────────────────────────

info "Starting services..."
docker compose -f "$COMPOSE_FILE" up -d --wait
ok "All services healthy"

# ── bootstrap first tenant ────────────────────────────────────────────────────

if $FIRST_RUN || $BOOTSTRAP; then
  echo ""
  info "Bootstrap first tenant"
  echo ""
  prompt BOOTSTRAP_TENANT   "Organisation name" "My MSP"
  prompt BOOTSTRAP_SLUG     "Slug (URL-safe, no spaces)" "my-msp"
  prompt BOOTSTRAP_EMAIL    "Admin email" ""
  prompt BOOTSTRAP_PASSWORD "Admin password (min 12 chars)" "" "true"

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
echo "  Dashboard    : http://${HOST}:${PORT}"
echo "  Agent enroll : create a token on the Enrollment page, then on the endpoint:"
echo "                 sudo rmmagent enroll --server http://${HOST}:${PORT} --token rmme_..."
echo ""
if [[ "${RMM_COOKIE_SECURE:-false}" != "true" ]]; then
  echo "  Note: RMM_COOKIE_SECURE=false (plain HTTP). Put this behind TLS and set"
  echo "        RMM_COOKIE_SECURE=true before exposing it to untrusted networks."
  echo ""
fi
