# Host agent (Linux, Proxmox VE)

LANLeecher learns MAC addresses from the switches' forwarding tables. When a switch port carries
several MAC addresses – a hypervisor with virtual machines, an access point, a small switch – the
switch only knows the total of that port, so LANLeecher books it as one **port group**. The
**LANLeecher agent** runs on such a host and reports the bytes of every MAC address it owns, so the
port can be split between them.

The switch stays authoritative:

- The agent's counters only **split** the traffic the switch counted on that port. They never add
  traffic and never create clients. A manipulated agent can at most shift traffic between the MAC
  addresses of its own port.
- Only MAC addresses that the switch currently sees on that port count. Traffic between guests of
  one host that never reaches the switch is not counted.
- Whatever the agents do not cover stays with the port group (for example the host itself if it
  runs no agent, or a guest that is not reported).
- A MAC address reported by two agents at the same time is ignored (see the agents page).

## Install

The packages are in the [package repository](https://repo.lan.party) (`lanleecher-agent` for
Debian/Ubuntu/Proxmox VE, Fedora/RHEL, Arch Linux, Alpine) and attached to every release.

```sh
sudo apt install lanleecher-agent          # or dnf / pacman / apk
```

1. In LANLeecher: **Admin → Agents → Add agent**. The page shows the token once, together with a
   ready-made configuration.
2. Put that configuration into `/etc/lanleecher/agent.conf` (owned by `root:lanleecher-agent`, mode
   `640`, because it contains the token).
3. Check it: `sudo lanleecher-agent test` lists what would be reported and sends one report.
4. Start it: `sudo systemctl enable --now lanleecher-agent`.

The agent runs as the unprivileged user `lanleecher-agent` in a hardened systemd unit. Revoking the
agent under Admin → Agents stops its reports immediately.

## Configuration (`/etc/lanleecher/agent.conf`)

```yaml
server: https://lanleecher.example   # LANLeecher's public_url
token: llk_...                       # from Admin → Agents (or token_file: /path)
interval: 15s                        # 5s … 10m
mode: auto                           # auto, linux or proxmox
interfaces:                          # optional shell globs on interface names
  include: []
  exclude: ["wlan*"]
tls:
  pinned_sha256: ""                  # only for a self-signed certificate (see below)
  ca_file: ""                        # or an internal CA
  insecure_skip_verify: false        # never in production
allow_insecure_http: false           # never in production
timeout: 10s
```

### Encryption

The connection is always encrypted and the server certificate is always checked, unless the
configuration explicitly says otherwise:

| Setup | What to configure |
|---|---|
| LANLeecher behind a reverse proxy with a valid certificate (Let's Encrypt, company CA in the system store) | nothing – `server: https://…` and the token |
| Internal CA | `tls.ca_file` |
| LANLeecher's built-in TLS with a self-signed certificate | `tls.pinned_sha256` (Admin → Agents shows it) |
| Tests only: any certificate | `tls.insecure_skip_verify: true` (logged as a warning) |
| Tests only: plain HTTP | `server: http://…` and `allow_insecure_http: true` (logged as a warning) |

The agent never follows redirects, so the token cannot be sent anywhere else. It honours
`HTTPS_PROXY`/`NO_PROXY`.

Behind a reverse proxy nothing special is needed: the agent sends `POST /api/v1/agent/report` with
`Authorization: Bearer …`, which nginx passes on by default; a report is at most 256 KiB (below
nginx's default `client_max_body_size` of 1 MiB). If the token has allowed IPs, LANLeecher must see
the agent's real address – set `trusted_proxies` (see [reverse-proxy.md](reverse-proxy.md)).

## What the agent reports

### `mode: linux` – the host itself

From `/sys/class/net`, per MAC address exactly one counter pair:

- Ethernet interfaces with a unicast MAC address that are up. Locally administered (random) MAC
  addresses are reported too – the switch sees them as well.
- Left out: loopback, interfaces without a MAC address (WireGuard, tun), bridge and bond members
  (counted on the bridge or bond), VLAN subinterfaces (counted on their parent), container plumbing
  (`veth*`, `docker*`, `virbr*`) and bridges without a physical port.
- A bridge with a physical port reports the traffic of the host itself.
- Received bytes are the MAC's download, sent bytes its upload.
- IP addresses are sent along (for the IP and hostname history, source `agent`); link-local
  (`fe80::/10`, `169.254.0.0/16`), loopback and multicast addresses are left out. The hostname of the
  host is sent as well.

### `mode: proxmox` – a Proxmox VE node and its guests

The host as above (`vmbr*` with its physical port), plus every **running** guest. This is the same
data Proxmox shows in its own graphs: `pvestatd` sums the counters of the host side of each guest
NIC, `tap<vmid>i<n>` for virtual machines and `veth<vmid>i<n>` for containers.

- The MAC addresses come from `/etc/pve/local/qemu-server/<vmid>.conf` (`netN: virtio=…`) and
  `/etc/pve/local/lxc/<vmid>.conf` (`netN: …,hwaddr=…`); snapshot sections are ignored.
- On the host side the direction is reversed: what the tap/veth device sends is the guest's
  download.
- The guest's name (`name:` / `hostname:`) is sent as its hostname; static container addresses
  (`ip=`, `ip6=`) as its IP addresses.
- Each node of a cluster reports its own guests. A live migration restarts the counters; LANLeecher
  treats that like a counter reset.
- `/etc/pve` belongs to `root:www-data`. On a Proxmox VE node the package adds a drop-in
  (`/etc/systemd/system/lanleecher-agent.service.d/proxmox.conf`) that gives only the agent service
  the group `www-data`.

`mode: auto` uses `proxmox` when `/etc/pve/local` exists.

## Limits

- Windows and libvirt/KVM without Proxmox VE are not supported yet.
- Agent counters only matter on ports with more than one MAC address. On a port with a single MAC
  address the switch already knows everything.
- After a restart of LANLeecher the first report of every interface only sets the starting point.
- At most 512 interfaces per report.

## API

`POST /api/v1/agent/report` with an agent token (scope `agent:report`). The format is described in
the [API reference](api.md) and in `openapi.yaml`. Agent tokens are created only on the agents page
and carry only this scope; web sessions never have it.
