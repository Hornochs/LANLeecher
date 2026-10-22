# LANLeecher

**Who's pulling the most?** At LAN parties, LANLeecher counts how much each client downloads and
uploads and turns it into rankings – in the browser, on the beamer and via API.

- Data via **SNMP v2c/v3** straight from the switches (64-bit counters), no agents on the clients
- **MAC address as the key**; IP and hostname (ARP, NetBIOS, mDNS, LLMNR, DNS) with history
- Robust against **counter resets, reboots and 64-bit wraps** – only deltas are stored
- **Events**: all data is kept, download and upload per device also historically
- **Uplink detection** via LLDP and MAC count; ports and devices can be hidden from rankings
- **Beamer view** `/board` with live updates, identical to the API `GET /api/v1/board`
- **REST API** with token scopes and an OpenAPI 3.1 specification
- A single static Go binary with built-in SQLite; web UI in English and German

## Installation

Packages for Debian/Ubuntu (`.deb`), Fedora/RHEL (`.rpm`), Arch Linux (`.pkg.tar.zst`) and Alpine
(`.apk`) for amd64, arm64 and armv7 are attached to every
[release](https://github.com/lan-dot-party/LANLeecher/releases).

```bash
sudo apt install ./lanleecher_*_amd64.deb
sudo nano /etc/lanleecher/config.yaml      # listen, public_url
sudo lanleecher user create admin --role admin
sudo systemctl enable --now lanleecher
```

Then log in, add the first switch under **Switches** using "Test connection", mark the uplinks and
start the event under **Events**.

### Docker

```bash
docker compose up -d        # see compose.yaml
docker compose exec lanleecher lanleecher user create admin --role admin
```

The image is published at `ghcr.io/lan-dot-party/lanleecher`. Host networking (as in
`compose.yaml`) lets the resolvers reach clients via NetBIOS and mDNS.

### From source

```bash
CGO_ENABLED=0 go build -o bin/lanleecher ./cmd/lanleecher
./bin/lanleecher -c config.yaml config init
./bin/lanleecher -c config.yaml user create admin --role admin
./bin/lanleecher -c config.yaml server
```

## Important: mark your uplinks

Ports to other switches or to the router must have the role **uplink**. Otherwise the traffic behind
them ends up on a "port group" and skews the rankings. LANLeecher marks suspicious ports (LLDP
neighbor or many MACs) as "probably uplink" – an admin has to confirm it.

## Reverse proxy

Run LANLeecher behind a TLS reverse proxy and set `public_url` to the `https://` URL so that session
cookies are marked Secure. Do not buffer `/api/v1/board/stream` (Server-Sent Events); see
[PROJECT_PLAN.md](PROJECT_PLAN.md#reverse-proxy-note-sse) for an nginx example.

## API

Interactive docs at `/api/`, the specification at `/api/openapi.json`, a summary for LLMs at
`/llms.txt`. Example:

```bash
curl -H "Authorization: Bearer llk_…" "https://lanleecher.example/api/v1/rankings/download?limit=10"
```

## Development

See [CLAUDE.md](CLAUDE.md) for architecture, conventions and test commands, and
[PROJECT_PLAN.md](PROJECT_PLAN.md) for the full design.

## License

MIT – see [LICENSE](LICENSE). Part of the [lan.party](https://lan.party) tools.
