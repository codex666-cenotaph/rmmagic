#!/usr/bin/env bash
#
# install-agent.sh — deploy the rmmagic endpoint agent on a Linux host.
#
# Walks through the full agent setup, prompting for anything it isn't
# given on the command line (or in the environment):
#   1. binary    build from source, or use a prebuilt binary, and install it
#   2. enroll    ask for the server URL + enrollment token, register the device
#   3. service   optionally install a systemd unit so the agent runs at boot
#
# Every prompt has a matching flag/env var, so the script is fully
# non-interactive when you supply them (handy for golden images / CI).
# Run with no arguments for a guided install.
#
# Usage:
#   sudo deploy/install-agent.sh [options]
#     --server URL        server base URL (e.g. https://rmm.example.com)
#     --token TOKEN       enrollment token (rmme_...)
#     --state-dir DIR     device identity directory   (default: /var/lib/rmmagent)
#     --bin PATH          prebuilt rmmagent binary (skip building from source)
#     --install-dir DIR   where to install the binary  (default: /usr/local/bin)
#     --service           install and start a systemd service (no prompt)
#     --no-service        do not install a service (no prompt)
#     --service-name NAME systemd unit name            (default: rmmagent)
#     --run-user USER     user the service runs as     (default: root)
#     --skip-enroll       binary/service only; device already enrolled
#     --yes               assume "yes" for prompts; never go interactive
#     -h, --help          show this help
#
# Secrets: the enrollment token is single-use and short-lived. Prefer
# passing it via the RMM_ENROLL_TOKEN env var (or the interactive prompt,
# which does not echo) over --token, so it stays out of your shell history.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# --- defaults --------------------------------------------------------------
SERVER="${RMM_SERVER:-}"
TOKEN="${RMM_ENROLL_TOKEN:-}"
STATE_DIR="${RMM_STATE_DIR:-/var/lib/rmmagent}"
PREBUILT_BIN="${RMM_AGENT_BIN:-}"
INSTALL_DIR="${RMM_INSTALL_DIR:-/usr/local/bin}"
SERVICE_NAME="${RMM_SERVICE_NAME:-rmmagent}"
RUN_USER="${RMM_RUN_USER:-root}"
WANT_SERVICE="ask"   # ask | yes | no
SKIP_ENROLL=0
ASSUME_YES=0

# --- helpers ---------------------------------------------------------------
log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARN\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mERROR\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# ask "prompt" "default" -> echoes the answer (default if non-interactive)
ask() {
  local prompt="$1" default="${2:-}" reply
  if [[ "$ASSUME_YES" == 1 || ! -t 0 ]]; then
    echo "$default"; return
  fi
  if [[ -n "$default" ]]; then
    read -r -p "$prompt [$default]: " reply </dev/tty || true
    echo "${reply:-$default}"
  else
    read -r -p "$prompt: " reply </dev/tty || true
    echo "$reply"
  fi
}

# confirm "prompt" "default(y/n)" -> returns 0 for yes, 1 for no
confirm() {
  local prompt="$1" default="${2:-y}" reply
  if [[ "$ASSUME_YES" == 1 || ! -t 0 ]]; then
    [[ "$default" == "y" ]]; return
  fi
  read -r -p "$prompt [$([[ $default == y ]] && echo Y/n || echo y/N)]: " reply </dev/tty || true
  reply="${reply:-$default}"
  [[ "$reply" =~ ^[Yy] ]]
}

# --- arg parsing -----------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --server)       SERVER="$2"; shift 2 ;;
    --token)        TOKEN="$2"; shift 2 ;;
    --state-dir)    STATE_DIR="$2"; shift 2 ;;
    --bin)          PREBUILT_BIN="$2"; shift 2 ;;
    --install-dir)  INSTALL_DIR="$2"; shift 2 ;;
    --service)      WANT_SERVICE="yes"; shift ;;
    --no-service)   WANT_SERVICE="no"; shift ;;
    --service-name) SERVICE_NAME="$2"; shift 2 ;;
    --run-user)     RUN_USER="$2"; shift 2 ;;
    --skip-enroll)  SKIP_ENROLL=1; shift ;;
    --yes|-y)       ASSUME_YES=1; shift ;;
    -h|--help)      sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

# --- privilege check -------------------------------------------------------
# Installing into system paths and writing /var/lib + systemd units needs
# root. Re-exec under sudo rather than failing late, unless the user clearly
# targeted user-writable paths.
if [[ "$(id -u)" != 0 ]]; then
  if have sudo; then
    warn "Not running as root — re-executing under sudo"
    exec sudo -E "$BASH" "${BASH_SOURCE[0]}" "$@"
  else
    warn "Not running as root and sudo not found; install may fail on system paths"
  fi
fi

# --- 1. binary -------------------------------------------------------------
INSTALLED_BIN="$INSTALL_DIR/rmmagent"

if [[ -n "$PREBUILT_BIN" ]]; then
  [[ -f "$PREBUILT_BIN" ]] || die "prebuilt binary not found: $PREBUILT_BIN"
  log "Installing prebuilt binary from $PREBUILT_BIN"
  install -D -m 0755 "$PREBUILT_BIN" "$INSTALLED_BIN"
else
  have go || die "go toolchain not found in PATH. Install Go 1.24+, or pass --bin PATH to use a prebuilt binary."
  log "Building rmmagent from source"

  VERSION="$(git -C "$REPO_ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
  COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
  VPKG="github.com/codex666-cenotaph/rmmagic/shared/version"
  LDFLAGS="-X ${VPKG}.Version=${VERSION} -X ${VPKG}.Commit=${COMMIT}"

  tmpbin="$(mktemp)"
  ( cd "$REPO_ROOT/agent" && CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o "$tmpbin" ./cmd/rmmagent )
  install -D -m 0755 "$tmpbin" "$INSTALLED_BIN"
  rm -f "$tmpbin"
  log "Installed rmmagent ${VERSION} -> $INSTALLED_BIN"
fi

"$INSTALLED_BIN" version || true

# --- 2. enroll -------------------------------------------------------------
if [[ "$SKIP_ENROLL" == 0 ]]; then
  # Already enrolled? identity.json lives in the state dir.
  if [[ -f "$STATE_DIR/identity.json" ]]; then
    if confirm "Device already enrolled in $STATE_DIR. Re-enroll?" "n"; then
      :
    else
      log "Keeping existing enrollment"
      SKIP_ENROLL=1
    fi
  fi
fi

if [[ "$SKIP_ENROLL" == 0 ]]; then
  [[ -n "$SERVER" ]] || SERVER="$(ask "Server base URL (e.g. https://rmm.example.com)")"
  [[ -n "$SERVER" ]] || die "server URL is required to enroll (pass --server or set RMM_SERVER)"

  if [[ -z "$TOKEN" ]]; then
    if [[ "$ASSUME_YES" == 1 || ! -t 0 ]]; then
      die "enrollment token required (pass --token or set RMM_ENROLL_TOKEN)"
    fi
    # Read without echo so the token does not land on screen / in scrollback.
    read -r -s -p "Enrollment token (rmme_...): " TOKEN </dev/tty || true
    echo
  fi
  [[ -n "$TOKEN" ]] || die "enrollment token is required"

  log "Enrolling with $SERVER (state-dir $STATE_DIR)"
  install -d -m 0700 "$STATE_DIR"
  "$INSTALLED_BIN" enroll --server "$SERVER" --token "$TOKEN" --state-dir "$STATE_DIR"
  log "Enrolled ✅"
fi

# --- 3. service ------------------------------------------------------------
case "$WANT_SERVICE" in
  ask)
    if have systemctl && confirm "Install and start a systemd service so the agent runs at boot?" "y"; then
      WANT_SERVICE="yes"
    else
      WANT_SERVICE="no"
    fi
    ;;
  yes)
    have systemctl || die "--service requested but systemctl not found (is this a systemd host?)"
    ;;
esac

if [[ "$WANT_SERVICE" == "yes" ]]; then
  unit="/etc/systemd/system/${SERVICE_NAME}.service"
  log "Writing systemd unit $unit"

  # The agent runs scripts and installs packages on the endpoint, so it
  # needs broad privilege when run as root — keep the hardening conservative.
  # When run as a non-root user, lock it down harder.
  hardening="NoNewPrivileges=false"
  if [[ "$RUN_USER" != "root" ]]; then
    hardening="NoNewPrivileges=true"
  fi

  cat >"$unit" <<EOF
[Unit]
Description=rmmagic endpoint agent
Documentation=https://github.com/codex666-cenotaph/rmmagic
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${RUN_USER}
ExecStart=${INSTALLED_BIN} run --state-dir ${STATE_DIR}
Restart=always
RestartSec=5s

# Hardening — the agent executes scripts/packages, so root installs keep
# privileges broad; tighten per your threat model.
${hardening}
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
ReadWritePaths=${STATE_DIR}

[Install]
WantedBy=multi-user.target
EOF

  # Make sure the state dir is owned by the run user when it isn't root.
  if [[ "$RUN_USER" != "root" ]]; then
    chown -R "$RUN_USER" "$STATE_DIR" 2>/dev/null || \
      warn "could not chown $STATE_DIR to $RUN_USER — fix ownership manually"
  fi

  systemctl daemon-reload
  systemctl enable --now "${SERVICE_NAME}.service"
  log "Service ${SERVICE_NAME} enabled and started ✅"
  echo
  log "Follow logs with:  journalctl -u ${SERVICE_NAME} -f"
  log "Check status with: systemctl status ${SERVICE_NAME}"
else
  echo
  log "No service installed. Run the agent in the foreground with:"
  echo "    ${INSTALLED_BIN} run --state-dir ${STATE_DIR}"
fi

log "Agent setup complete."
