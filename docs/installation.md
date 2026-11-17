# Installation

LANLeecher is a single binary with a built-in SQLite database. It needs no database server and no
agent on the clients – only SNMP access (UDP 161) to the switches.

## Requirements

- Linux on amd64, arm64 or armv7 (a Raspberry Pi is enough for a large LAN party).
- Switches that answer SNMP v2c or v3 with 64-bit interface counters (`ifHCInOctets`,
  `ifHCOutOctets`) and a forwarding table (BRIDGE-MIB or Q-BRIDGE-MIB). See
  [switch-compatibility.md](switch-compatibility.md).
- For hostnames: the LANLeecher host must reach the client IPs (NetBIOS UDP 137, mDNS UDP 5353,
  LLMNR UDP 5355). Multicast mDNS and the local neighbor table only cover the host's own segment.

## Packages

Every [release](https://github.com/lan-dot-party/LANLeecher/releases) carries packages for amd64,
arm64 and armv7, plus `SHA256SUMS` and a build provenance attestation.

```bash
# Debian, Ubuntu
sudo apt install ./lanleecher_<version>_amd64.deb
# Fedora, Rocky/RHEL
sudo dnf install ./lanleecher_<version>_amd64.rpm
# Arch Linux
sudo pacman -U lanleecher_<version>_amd64.pkg.tar.zst
# Alpine (no systemd unit is started there; run "lanleecher server" with your init system)
sudo apk add --allow-untrusted lanleecher_<version>_amd64.apk
```

Verify a download:

```bash
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify lanleecher_<version>_amd64.deb --repo lan-dot-party/LANLeecher
```

The package installs:

| Path | Content |
| --- | --- |
| `/usr/bin/lanleecher` | the binary |
| `/etc/lanleecher/config.yaml` | configuration (kept on updates) |
| `/etc/lanleecher/secret.key` | key that encrypts SNMP credentials, created on first install |
| `/var/lib/lanleecher/` | SQLite database and backups |
| `/usr/lib/systemd/system/lanleecher.service` | hardened systemd unit, runs as user `lanleecher` |

The service is **not** started automatically. First steps:

```bash
sudo nano /etc/lanleecher/config.yaml          # listen, public_url
sudo lanleecher user create admin --role admin  # asks for a password (min. 10 characters)
sudo systemctl enable --now lanleecher
```

Then open `public_url`, log in and continue with the [LAN party guide](lan-party-guide.md).

## Docker

The image `ghcr.io/lan-dot-party/lanleecher` exists for amd64, arm64 and armv7 (tags `latest` and the
plain version, e.g. `1.2.3`; pin a version for production). It runs as user `lanleecher` (UID 100,
GID 101) and keeps the database `lanleecher.db` and the key `secret.key` in the volume
`/var/lib/lanleecher`. The built-in configuration `/etc/lanleecher/config.yaml` listens on
`0.0.0.0:9040` with `public_url: http://localhost:9040`; mount your own file over it to change
values. There is no environment variable for `listen` or `public_url`; `TZ` sets the time zone.

**Use HTTPS on a LAN party.** The built-in configuration serves plain HTTP on every interface, so
passwords, session cookies and API tokens cross the network in clear text, and attendees on the same
network can read them. Put the container behind an HTTPS reverse proxy (see
[reverse-proxy.md](reverse-proxy.md)) with `public_url: https://…`, or set `tls.cert_file` and
`tls.key_file`. LANLeecher logs a warning at start while it serves plain HTTP on a network address.

### Docker Compose

The repository ships two examples that differ only in where the data lives:

| File | Data | Choose it when |
| --- | --- | --- |
| `compose.yaml` | named volume `lanleecher-data` | Docker should manage the data; nothing to prepare |
| `compose.bind.yaml` | local directory `./data` | you want the data visible on the host and easy to back up; the directory must belong to UID 100 / GID 101 |

For the local-path variant, prepare the directory once and add `-f compose.bind.yaml` to every
`docker compose` command below:

```bash
mkdir -p data && sudo chown 100:101 data
```

```bash
docker compose up -d                                                      # start
docker compose exec lanleecher lanleecher user create admin --role admin  # first admin (asks for a password)
docker compose logs -f lanleecher                                         # logs
docker compose pull && docker compose up -d                               # update
```

Take a backup before every update. `lanleecher backup` writes a consistent copy of the database
while the server runs, into `backups/` in the data directory:

```bash
docker compose exec lanleecher lanleecher backup                          # → /var/lib/lanleecher/backups/lanleecher-<date-time>.db
docker compose cp lanleecher:/var/lib/lanleecher/backups ./backups        # named volume: copy it out
docker compose cp lanleecher:/var/lib/lanleecher/secret.key ./secret.key  # and the key
```

With `compose.bind.yaml` the files are already in `./data/backups/`; back up `./data` (including
`secret.key`). Never copy the live `lanleecher.db` while the container runs – use the backup command.
`secret.key` must be backed up together with the database: without it the stored SNMP credentials
cannot be decrypted. The Docker data volume holds `secret.key` next to the database, so every backup
or snapshot of that volume contains the key too: protect it like the key itself (restrict access,
encrypt it, keep it off shared storage).

Both examples use **host networking**, so the resolvers can reach clients via NetBIOS and mDNS and
read the host's neighbor table. `ports:` has no effect then: the service listens on the host's
port 9040, on all addresses by default. To change that, mount your own configuration read-only
(`./config.yaml:/etc/lanleecher/config.yaml:ro`, see the commented line in the compose files) and set
`listen: 127.0.0.1:9040` when a reverse proxy runs on the same host. Set `public_url` to the address
users open, e.g. `https://leecher.example.org`; with `https://` session cookies are marked
`Secure`. Keep `trusted_proxies` at the proxy's address so client IPs are taken from its headers.
Start from the built-in configuration (`packaging/docker-config.yaml`), which keeps `secret_key_file` and
the database path inside the data volume.

## From source

Go 1.26 or newer:

```bash
git clone https://github.com/lan-dot-party/LANLeecher.git
cd LANLeecher
CGO_ENABLED=0 go build -o bin/lanleecher ./cmd/lanleecher
./bin/lanleecher -c config.yaml config init     # writes config.yaml and secret.key next to it
./bin/lanleecher -c config.yaml user create admin --role admin
./bin/lanleecher -c config.yaml server
```

## Command line

| Command | Purpose |
| --- | --- |
| `lanleecher server` | run poller, web UI and API |
| `lanleecher config init [--force]` | write an example configuration and create `secret.key` |
| `lanleecher migrate` | apply database migrations (the server does this on start) |
| `lanleecher backup [file]` | consistent copy of the database while the server runs |
| `lanleecher user create <name> --role viewer\|operator\|admin [--language en\|de]` | create a user |
| `lanleecher user passwd <name>` | set a new password (ends the user's sessions) |
| `lanleecher user list` | list users |
| `lanleecher event start "<name>" [--notes …]` / `event stop` / `event list` | manage events |
| `lanleecher event schedule "<name>" --start YYYY-MM-DD --end YYYY-MM-DD [--start-time HH:MM] [--end-time HH:MM]` / `event schedules [--all]` / `event unschedule <id>` | plan events that start and stop by themselves (local time zone, last day inclusive) |
| `lanleecher switch add --name … --host … [--port 161] [--arp]` / `switch list` | add an SNMP v2c switch |
| `lanleecher version` | print the version |

All commands accept `-c/--config` (default `/etc/lanleecher/config.yaml`, or `LANLEECHER_CONFIG`).
Passwords are read from a prompt or, when piped, from stdin. `switch add` reads the community from
`LANLEECHER_SNMP_COMMUNITY` or stdin, never from an argument (arguments are visible in the process
list). Run as root, the CLI hands files it creates to the owner of the data directory, so
`sudo lanleecher …` is safe.

`systemctl reload lanleecher` (SIGHUP) reloads the switch list without a restart.

## Backup

```bash
sudo -u lanleecher lanleecher backup        # → /var/lib/lanleecher/backups/lanleecher-<date-time>.db
```

The backup uses `VACUUM INTO` and is consistent while the server runs. Keep `/etc/lanleecher/secret.key`
as well – without it the stored SNMP credentials cannot be decrypted.

## Upgrade

Install the new package. Configuration, key and database are kept; a running service is restarted
and applies database migrations on start. Take a backup first. To roll back, install the previous
package and restore the backup over `/var/lib/lanleecher/lanleecher.db` while the service is stopped.

## Uninstall

`apt remove lanleecher` (or the equivalent) keeps configuration and data; `apt purge lanleecher`
deletes `/etc/lanleecher`, `/var/lib/lanleecher` and the user.
