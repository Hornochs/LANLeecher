#!/bin/sh
# LANLeecher agent pre-removal script; updates keep the service running.
set -e
case "$1" in
    upgrade | 1) exit 0 ;;
esac
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl disable --now lanleecher-agent.service >/dev/null 2>&1 || true
fi
exit 0
