#!/bin/sh
# LANLeecher agent post-installation script (deb, rpm, archlinux, apk).
# Creates the system user and, on Proxmox VE, lets the service read the
# guest configurations. The service is not started automatically.
set -e

if ! id -u lanleecher-agent >/dev/null 2>&1; then
    if command -v systemd-sysusers >/dev/null 2>&1 && [ -f /usr/lib/sysusers.d/lanleecher-agent.conf ]; then
        systemd-sysusers /usr/lib/sysusers.d/lanleecher-agent.conf
    elif command -v useradd >/dev/null 2>&1; then
        useradd --system --no-create-home --home-dir / --shell /usr/sbin/nologin --user-group lanleecher-agent 2>/dev/null ||
            useradd --system --no-create-home --home-dir / --shell /sbin/nologin lanleecher-agent
    else
        addgroup -S lanleecher-agent 2>/dev/null || true
        adduser -S -D -H -h / -s /sbin/nologin -G lanleecher-agent lanleecher-agent
    fi
fi

# /etc/lanleecher may also belong to the LANLeecher server package (0751):
# traversable, not listable. The configuration holds the token.
if [ ! -d /etc/lanleecher ]; then
    mkdir -p /etc/lanleecher
    chmod 0751 /etc/lanleecher
fi
chown root:lanleecher-agent /etc/lanleecher/agent.conf
chmod 0640 /etc/lanleecher/agent.conf

# Proxmox VE: /etc/pve belongs to root:www-data (0640). Only the agent
# service gets the group, not the user account.
dropin=/etc/systemd/system/lanleecher-agent.service.d/proxmox.conf
if [ -d /etc/pve ] && getent group www-data >/dev/null 2>&1 && [ ! -f "$dropin" ]; then
    mkdir -p "$(dirname "$dropin")"
    cat > "$dropin" <<'UNIT'
# Created by the lanleecher-agent package on a Proxmox VE node: read the
# guest configurations in /etc/pve (root:www-data, 0640).
[Service]
SupplementaryGroups=www-data
UNIT
fi

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
    systemctl try-restart lanleecher-agent.service || true
fi

if grep -q 'llk_REPLACE_ME' /etc/lanleecher/agent.conf 2>/dev/null; then
    cat <<'MSG'

LANLeecher agent is installed. Next steps:
  1. In LANLeecher: Admin → Agents → add an agent; copy the configuration.
  2. Paste it into /etc/lanleecher/agent.conf (sudo nano /etc/lanleecher/agent.conf).
  3. Check it:       sudo lanleecher-agent test
  4. Start it:       sudo systemctl enable --now lanleecher-agent

MSG
fi
exit 0
