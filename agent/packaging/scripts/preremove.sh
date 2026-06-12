#!/bin/sh
# preremove — runs as deb prerm / rpm %preun.
#
# Stop and disable the service on a real removal, but leave it running
# across an upgrade so the agent stays connected.
#   deb: $1 = "remove" | "purge" | "upgrade"
#   rpm: $1 = 0 (final removal) | 1 (upgrade)
set -e

remove=0
case "$1" in
  remove|purge) remove=1 ;;           # deb removal
  0) remove=1 ;;                      # rpm final removal
esac

if [ "$remove" -eq 1 ] && command -v systemctl >/dev/null 2>&1; then
  systemctl disable --now rmmagent.service >/dev/null 2>&1 || true
fi

exit 0
