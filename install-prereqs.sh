#!/usr/bin/env bash
set -euo pipefail

GO_VERSION="1.24.4"
NODE_MAJOR="22"

# ── helpers ──────────────────────────────────────────────────────────────────

info()  { printf '\e[1;34m[info]\e[0m  %s\n' "$*"; }
ok()    { printf '\e[1;32m[ok]\e[0m    %s\n' "$*"; }
warn()  { printf '\e[1;33m[warn]\e[0m  %s\n' "$*"; }
die()   { printf '\e[1;31m[error]\e[0m %s\n' "$*" >&2; exit 1; }

need_cmd() { command -v "$1" &>/dev/null || die "required command not found: $1 — install it and re-run"; }

# ── detect OS ────────────────────────────────────────────────────────────────

detect_os() {
  if [[ -f /etc/os-release ]]; then
    # shellcheck source=/dev/null
    source /etc/os-release
    OS_ID="${ID:-unknown}"
    OS_ID_LIKE="${ID_LIKE:-}"
  elif [[ "$(uname)" == "Darwin" ]]; then
    OS_ID="macos"
  else
    die "Cannot detect OS. Supported: Ubuntu/Debian, Fedora/RHEL/Rocky, macOS."
  fi
}

is_debian_like() { [[ "$OS_ID" == "ubuntu" || "$OS_ID" == "debian" || "$OS_ID_LIKE" == *"debian"* ]]; }
is_fedora_like()  { [[ "$OS_ID" == "fedora" || "$OS_ID" == "rhel"   || "$OS_ID" == "rocky" || "$OS_ID" == "almalinux" || "$OS_ID_LIKE" == *"rhel"* || "$OS_ID_LIKE" == *"fedora"* ]]; }
is_macos()        { [[ "$OS_ID" == "macos" ]]; }

# ── package manager wrappers ─────────────────────────────────────────────────

pkg_install() {
  if is_debian_like; then
    sudo apt-get install -y "$@"
  elif is_fedora_like; then
    sudo dnf install -y "$@"
  elif is_macos; then
    brew install "$@"
  fi
}

# ── Go ───────────────────────────────────────────────────────────────────────

install_go() {
  if command -v go &>/dev/null; then
    current=$(go version | awk '{print $3}' | sed 's/go//')
    # compare major.minor only
    current_mm=$(echo "$current" | cut -d. -f1,2)
    need_mm=$(echo "$GO_VERSION" | cut -d. -f1,2)
    if [[ "$(printf '%s\n' "$need_mm" "$current_mm" | sort -V | head -1)" == "$need_mm" ]]; then
      ok "Go $current already installed — skipping"
      return
    fi
    warn "Go $current found but need $GO_VERSION+ — installing fresh"
  fi

  info "Installing Go $GO_VERSION …"
  local arch
  arch=$(uname -m)
  case "$arch" in
    x86_64)  arch="amd64" ;;
    aarch64) arch="arm64" ;;
    arm64)   arch="arm64" ;;
    *)       die "Unsupported architecture: $arch" ;;
  esac

  local tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
  if is_macos; then
    tarball="go${GO_VERSION}.darwin-${arch}.tar.gz"
  fi

  local url="https://go.dev/dl/${tarball}"
  local tmp
  tmp=$(mktemp -d)
  curl -fsSL "$url" -o "$tmp/$tarball"
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf "$tmp/$tarball"
  rm -rf "$tmp"

  # add to PATH for this session
  export PATH="/usr/local/go/bin:$PATH"

  # persist in shell rc if not already there
  for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
    if [[ -f "$rc" ]] && ! grep -q '/usr/local/go/bin' "$rc"; then
      echo 'export PATH="/usr/local/go/bin:$PATH"' >> "$rc"
      info "Added Go to PATH in $rc"
    fi
  done

  ok "Go $(go version) installed"
}

# ── Docker ───────────────────────────────────────────────────────────────────

install_docker() {
  if command -v docker &>/dev/null; then
    ok "Docker $(docker --version | awk '{print $3}' | tr -d ',') already installed — skipping"
    return
  fi

  info "Installing Docker …"

  if is_debian_like; then
    need_cmd curl
    curl -fsSL https://get.docker.com | sudo sh
  elif is_fedora_like; then
    sudo dnf -y install dnf-plugins-core
    sudo dnf config-manager --add-repo https://download.docker.com/linux/fedora/docker-ce.repo
    sudo dnf install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
  elif is_macos; then
    warn "On macOS, install Docker Desktop manually from https://www.docker.com/products/docker-desktop/"
    warn "Skipping Docker install."
    return
  fi

  sudo systemctl enable --now docker 2>/dev/null || true

  # add current user to docker group so no sudo needed
  if ! groups | grep -qw docker; then
    sudo usermod -aG docker "$USER"
    warn "Added $USER to the 'docker' group. You may need to log out and back in (or run 'newgrp docker') for this to take effect."
  fi

  ok "Docker installed"
}

# ── Node.js ──────────────────────────────────────────────────────────────────

install_node() {
  if command -v node &>/dev/null; then
    current_major=$(node --version | sed 's/v//' | cut -d. -f1)
    if (( current_major >= NODE_MAJOR )); then
      ok "Node.js $(node --version) already installed — skipping"
      return
    fi
    warn "Node.js $(node --version) found but need v${NODE_MAJOR}+ — upgrading via NodeSource"
  fi

  info "Installing Node.js $NODE_MAJOR …"

  if is_debian_like; then
    need_cmd curl
    curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | sudo -E bash -
    sudo apt-get install -y nodejs
  elif is_fedora_like; then
    curl -fsSL "https://rpm.nodesource.com/setup_${NODE_MAJOR}.x" | sudo -E bash -
    sudo dnf install -y nodejs
  elif is_macos; then
    brew install "node@${NODE_MAJOR}"
    brew link --overwrite "node@${NODE_MAJOR}" || true
  fi

  ok "Node.js $(node --version) installed"
}

# ── golang-migrate ────────────────────────────────────────────────────────────

install_migrate() {
  if command -v migrate &>/dev/null; then
    ok "golang-migrate $(migrate -version 2>&1 | head -1) already installed — skipping"
    return
  fi

  info "Installing golang-migrate …"

  local arch
  arch=$(uname -m)
  case "$arch" in
    x86_64)  arch="amd64" ;;
    aarch64) arch="arm64" ;;
    arm64)   arch="arm64" ;;
  esac

  local os="linux"
  if is_macos; then os="darwin"; fi

  local version
  version=$(curl -fsSL https://api.github.com/repos/golang-migrate/migrate/releases/latest | grep '"tag_name"' | sed 's/.*"v\([^"]*\)".*/\1/')
  local url="https://github.com/golang-migrate/migrate/releases/download/v${version}/migrate.${os}-${arch}.tar.gz"

  local tmp
  tmp=$(mktemp -d)
  curl -fsSL "$url" -o "$tmp/migrate.tar.gz"
  tar -xzf "$tmp/migrate.tar.gz" -C "$tmp"
  sudo mv "$tmp/migrate" /usr/local/bin/migrate
  sudo chmod +x /usr/local/bin/migrate
  rm -rf "$tmp"

  ok "golang-migrate $version installed"
}

# ── main ─────────────────────────────────────────────────────────────────────

main() {
  detect_os
  info "Detected OS: $OS_ID"

  if is_macos; then
    if ! command -v brew &>/dev/null; then
      die "Homebrew not found. Install it first: https://brew.sh"
    fi
  fi

  need_cmd curl

  if is_debian_like; then
    info "Updating apt …"
    sudo apt-get update -qq
  fi

  install_go
  install_docker
  install_node
  install_migrate

  echo ""
  ok "All prerequisites installed."
  echo ""
  echo "  Next steps:"
  echo "    make dev-stack    # start Postgres, NATS, MinIO, Mailpit"
  echo "    make migrate-up   # apply DB migrations"
  echo ""
  if ! groups | grep -qw docker 2>/dev/null; then
    warn "Remember: log out and back in (or run 'newgrp docker') so the docker group takes effect."
  fi
}

main "$@"
