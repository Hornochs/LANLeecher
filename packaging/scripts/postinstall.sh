#!/bin/sh
# LANLeecher post-installation script (deb, rpm, archlinux, apk).
# Creates the system user, the data directory and secret.key. The service is
# not started automatically: the admin configures it first.
set -e

if ! id -u lanleecher >/dev/null 2>&1; then
    if command -v systemd-sysusers >/dev/null 2>&1 && [ -f /usr/lib/sysusers.d/lanleecher.conf ]; then
        systemd-sysusers /usr/lib/sysusers.d/lanleecher.conf
    elif command -v useradd >/dev/null 2>&1; then
        useradd --system --home-dir /var/lib/lanleecher --no-create-home \
            --shell /usr/sbin/nologin --user-group lanleecher 2>/dev/null ||
            useradd --system --home-dir /var/lib/lanleecher --no-create-home --shell /sbin/nologin lanleecher
    else
        addgroup -S lanleecher 2>/dev/null || true
        adduser -S -D -H -h /var/lib/lanleecher -s /sbin/nologin -G lanleecher lanleecher
    fi
fi

mkdir -p /var/lib/lanleecher /etc/lanleecher
chown lanleecher:lanleecher /var/lib/lanleecher
chmod 0750 /var/lib/lanleecher
chown root:lanleecher /etc/lanleecher
chmod 0750 /etc/lanleecher
chown root:lanleecher /etc/lanleecher/config.yaml
chmod 0640 /etc/lanleecher/config.yaml

# The key encrypts SNMP and DHCP credentials in the database. It is created
# once and never replaced by updates.
if [ ! -s /etc/lanleecher/secret.key ]; then
    umask 027
    od -An -tx1 -N32 /dev/urandom | tr -d ' \n' > /etc/lanleecher/secret.key
    echo >> /etc/lanleecher/secret.key
    chown root:lanleecher /etc/lanleecher/secret.key
    chmod 0640 /etc/lanleecher/secret.key
fi

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
    # Updates restart a running service; migrations run on start.
    systemctl try-restart lanleecher.service || true
fi

if [ ! -f /var/lib/lanleecher/lanleecher.db ]; then
    cat <<'EOF'

LANLeecher is installed. Next steps:
  1. Check the configuration: sudo nano /etc/lanleecher/config.yaml   (listen, public_url)
  2. Create the first admin:  sudo lanleecher user create admin --role admin
  3. Start the service:       sudo systemctl enable --now lanleecher
  4. Log in and add the first switch under "Switches".

EOF
fi
exit 0
