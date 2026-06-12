#!/bin/sh
# postremove — runs as deb postrm / rpm %postun.
#
# Reload systemd after the unit file is gone. On a Debian *purge* also drop
# the state directory (device identity + command journal); a plain remove or
# an upgrade keeps it so re-installing resumes the same identity.
#   deb: $1 = "remove" | "purge" | "upgrade" | ...
#   rpm: $1 = 0 (final removal) | 1 (upgrade)
set -e

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi

if [ "$1" = "purge" ]; then
  rm -rf /var/lib/rmmagent
fi

exit 0
