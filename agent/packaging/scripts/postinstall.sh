#!/bin/sh
# postinstall — runs as deb postinst / rpm %post.
#
# Argument conventions differ by format, so detect install-vs-upgrade for
# both:
#   deb: $1 = "configure", $2 = previous version (non-empty only on upgrade)
#   rpm: $1 = number of installed packages (1 = fresh install, 2 = upgrade)
set -e

upgrade=0
if [ "$1" = "configure" ]; then
  [ -n "$2" ] && upgrade=1            # deb upgrade
elif [ "$1" -ge 2 ] 2>/dev/null; then
  upgrade=1                           # rpm upgrade
fi

# Pick up the new unit file in all cases.
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi

if [ "$upgrade" -eq 1 ]; then
  # Only restart if it was already running (i.e. the device is enrolled and
  # the operator had it active); never auto-start on a fresh box.
  if command -v systemctl >/dev/null 2>&1; then
    systemctl try-restart rmmagent.service >/dev/null 2>&1 || true
  fi
else
  cat <<'EOF'
rmmagent installed. The agent is not started yet — enroll the device first:

  sudo rmmagent enroll --server https://YOUR_SERVER --token rmme_...
  sudo systemctl enable --now rmmagent

Create the enrollment token on the dashboard's Enrollment page.
EOF
fi

exit 0
