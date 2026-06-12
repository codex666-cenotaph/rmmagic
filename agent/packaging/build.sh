#!/usr/bin/env bash
#
# build.sh — build static rmmagent binaries and package the Linux ones as
# .deb/.rpm.
#
# Produces, for each os/arch target, a stripped static binary under
# agent/packaging/dist/ (rmmagent-<os>-<arch>[.exe]); Linux targets
# additionally get a Debian and an RPM package. The version is derived
# from `git describe` (override with VERSION=…).
#
# Usage:
#   agent/packaging/build.sh [--targets "linux/amd64 windows/amd64"] [--bin-only]
#
# Env overrides:
#   VERSION   package/binary version       (default: git describe, sans "v")
#   TARGETS   space-separated GOOS/GOARCH  (default: linux/amd64 linux/arm64 windows/amd64)
#   DIST      output directory             (default: agent/packaging/dist)
#
# Requires: go, and (unless --bin-only or no linux targets) nfpm
#   go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest

set -euo pipefail

PKG_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AGENT_DIR="$(cd "$PKG_DIR/.." && pwd)"
REPO_ROOT="$(cd "$AGENT_DIR/.." && pwd)"

TARGETS="${TARGETS:-linux/amd64 linux/arm64 windows/amd64}"
DIST="${DIST:-$PKG_DIR/dist}"
BIN_ONLY=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --targets)  TARGETS="$2"; shift 2 ;;
    --bin-only) BIN_ONLY=1; shift ;;
    -h|--help)  sed -n '2,21p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mERROR\033[0m %s\n' "$*" >&2; exit 1; }
have(){ command -v "$1" >/dev/null 2>&1; }

have go || die "go toolchain not found in PATH"
if [[ "$BIN_ONLY" == 0 && "$TARGETS" == *linux/* ]]; then
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
log "Version ${VERSION} (commit ${COMMIT}); targets: ${TARGETS}"

for target in $TARGETS; do
  goos="${target%/*}"; goarch="${target#*/}"
  [[ "$goos" == "$target" || -z "$goos" || -z "$goarch" ]] && die "bad target '$target' (want GOOS/GOARCH, e.g. linux/amd64)"

  ext=""; [[ "$goos" == windows ]] && ext=".exe"
  out="$DIST/rmmagent-${goos}-${goarch}${ext}"

  log "Building rmmagent for ${goos}/${goarch}"
  ( cd "$AGENT_DIR" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/rmmagent )

  # deb/rpm packaging applies to Linux targets only (Windows gets an MSI in
  # a later phase; until then the .exe itself is the artifact).
  [[ "$BIN_ONLY" == 1 || "$goos" != linux ]] && continue

  # Stage this arch's binary at the fixed path nfpm.yaml references, then run
  # nfpm from the packaging dir so its relative `src` paths resolve.
  mkdir -p "$PKG_DIR/.staging"
  cp "$out" "$PKG_DIR/.staging/rmmagent"
  for packager in deb rpm; do
    log "Packaging .${packager} for ${goarch}"
    ( cd "$PKG_DIR" && PKG_VERSION="$VERSION" PKG_ARCH="$goarch" \
        nfpm package --config nfpm.yaml --packager "$packager" --target "$DIST" )
  done
  rm -rf "$PKG_DIR/.staging"
done

log "Artifacts in $DIST:"
ls -1 "$DIST"
