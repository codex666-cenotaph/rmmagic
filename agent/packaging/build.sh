#!/usr/bin/env bash
#
# build.sh — build static rmmagent binaries and package them as .deb/.rpm.
#
# Produces, for each target architecture, a stripped static binary plus a
# Debian and an RPM package, all under agent/packaging/dist/. The version
# is derived from `git describe` (override with VERSION=…).
#
# Usage:
#   agent/packaging/build.sh [--arches "amd64 arm64"] [--bin-only]
#
# Env overrides:
#   VERSION   package/binary version       (default: git describe, sans "v")
#   ARCHES    space-separated GOARCH list   (default: amd64 arm64)
#   DIST      output directory              (default: agent/packaging/dist)
#
# Requires: go, and (unless --bin-only) nfpm
#   go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest

set -euo pipefail

PKG_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AGENT_DIR="$(cd "$PKG_DIR/.." && pwd)"
REPO_ROOT="$(cd "$AGENT_DIR/.." && pwd)"

ARCHES="${ARCHES:-amd64 arm64}"
DIST="${DIST:-$PKG_DIR/dist}"
BIN_ONLY=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arches)   ARCHES="$2"; shift 2 ;;
    --bin-only) BIN_ONLY=1; shift ;;
    -h|--help)  sed -n '2,22p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mERROR\033[0m %s\n' "$*" >&2; exit 1; }
have(){ command -v "$1" >/dev/null 2>&1; }

have go || die "go toolchain not found in PATH"
if [[ "$BIN_ONLY" == 0 ]]; then
  have nfpm || die "nfpm not found. Install: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest (or pass --bin-only)"
fi

# Version: prefer an explicit VERSION, else git describe with the leading
# "v" stripped. nfpm wants semver, so synthesize one for untagged builds
# (e.g. "abc1234-dirty" -> "0.0.0-abc1234-dirty", a valid semver prerelease).
if [[ -z "${VERSION:-}" ]]; then
  raw="$(git -C "$REPO_ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
  raw="${raw#v}"
  # Keep proper "X.Y[.Z]…" tags verbatim; turn anything else (bare commit,
  # "abc123-dirty") into a valid semver prerelease so nfpm accepts it.
  if [[ "$raw" =~ ^[0-9]+\.[0-9]+ ]]; then
    VERSION="$raw"
  else
    VERSION="0.0.0-$raw"
  fi
fi
COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
VPKG="github.com/codex666-cenotaph/rmmagic/shared/version"
LDFLAGS="-s -w -X ${VPKG}.Version=${VERSION} -X ${VPKG}.Commit=${COMMIT}"

rm -rf "$DIST"; mkdir -p "$DIST"
log "Version ${VERSION} (commit ${COMMIT}); arches: ${ARCHES}"

for arch in $ARCHES; do
  log "Building rmmagent for linux/${arch}"
  ( cd "$AGENT_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
      go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/rmmagent-${arch}" ./cmd/rmmagent )

  [[ "$BIN_ONLY" == 1 ]] && continue

  # Stage this arch's binary at the fixed path nfpm.yaml references, then run
  # nfpm from the packaging dir so its relative `src` paths resolve.
  mkdir -p "$PKG_DIR/.staging"
  cp "$DIST/rmmagent-${arch}" "$PKG_DIR/.staging/rmmagent"
  for packager in deb rpm; do
    log "Packaging .${packager} for ${arch}"
    ( cd "$PKG_DIR" && PKG_VERSION="$VERSION" PKG_ARCH="$arch" \
        nfpm package --config nfpm.yaml --packager "$packager" --target "$DIST" )
  done
  rm -rf "$PKG_DIR/.staging"
done

log "Artifacts in $DIST:"
ls -1 "$DIST"
