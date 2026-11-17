#!/bin/sh
# LANLeecher agent post-removal script. The configuration is kept on
# removal; "apt purge" deletes it and the Proxmox drop-in.
set -e
if [ "$1" = "purge" ]; then
    rm -f /etc/lanleecher/agent.conf
    rm -rf /etc/systemd/system/lanleecher-agent.service.d
    rmdir /etc/lanleecher 2>/dev/null || true
    if id -u lanleecher-agent >/dev/null 2>&1; then
        userdel lanleecher-agent 2>/dev/null || true
    fi
fi
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
fi
exit 0
