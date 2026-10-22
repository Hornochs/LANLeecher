#!/bin/sh
# LANLeecher post-removal script. Data in /var/lib/lanleecher and the
# configuration are kept on removal; "apt purge" deletes them.
set -e

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
fi

if [ "$1" = "purge" ]; then
    rm -rf /var/lib/lanleecher /etc/lanleecher
    if id -u lanleecher >/dev/null 2>&1; then
        userdel lanleecher 2>/dev/null || true
    fi
fi
exit 0
