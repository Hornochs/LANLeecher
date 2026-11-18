# Changelog

All notable changes to this project are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.4.1] - 2026-11-18

### Fixed

- **Network map:** adding, moving and removing switches and drawing or hiding links failed with
  403, because the page did not hand the CSRF token to `map.js` (`<body data-csrf>`).
- **Network map:** a newly added switch or internet connection is placed on a free spot near the
  centre instead of exactly on top of the previous one.
- **Network map:** switches that other switches learn only by their port or VLAN interface MACs are
  recognised now (`ifPhysAddress` is read with the forwarding table), and a switch behind another
  one on the same port is no longer drawn as directly linked when a third switch sees both on
  different ports, which also works for switches without a MAC table. Ported from ERS-Proxy.
- **SNMP:** a walk that the agent answers out of order (TP-Link T1500G while its MAC table changes)
  is tried once more instead of failing the forwarding table read.

### Changed

- README: status "in use", screenshots of the network map (light and dark) and of the internet
  connection tiles (`docs/images/`, taken from a demo setup), ERS-Proxy and LANventory under
  related projects.

## [1.4.0] - 2026-11-17

### Security

- **MAC flooding:** a port with more than `polling.max_port_macs` (512) MACs in one forwarding-table
  read creates no new devices (known MACs are still updated; the log warns), and MACs that never had
  traffic and were not seen for `polling.idle_mac_retention` (24 h) are deleted hourly. Migration 00010.
- **Login:** the lockout locks the pair of client address and user name after 5 failures and a
  client address after 20 failures across user names, never a user name for everyone, so nobody can
  lock the admin out. Behind a reverse proxy the client address comes from `X-Forwarded-For` read
  right to left (trusted proxies skipped), `X-Real-IP` only as fallback, with a warning when proxy
  headers arrive from an address outside `trusted_proxies`. The login form is limited to 8 KiB and
  the user name to 64 characters; only local paths are accepted as the page after login.
- Board stream (`/api/v1/board/stream`, public with `board.public`): at most 512 open streams, 8 per
  client address (429 beyond that), and a client that stops reading is dropped after 15 s.
- A warning at start when LANLeecher serves plain HTTP on a network address (the Docker default),
  and a Docker note on HTTPS.
- **Cloned MACs:** while the latest forwarding-table reads show a MAC on two client ports at once,
  the traffic of those ports is booked as unassigned instead of crediting it to the MAC, and the
  log warns. A device that moves to another port is credited again after the old switch's next
  read. Port security on the switches remains the real protection (docs/concepts.md).

- Build with Go 1.26.11 (was 1.24.7) and current `golang.org/x/*` modules. `govulncheck` reported
  about 47 reachable standard-library vulnerabilities (`net/http`, `crypto/x509`) for the old
  toolchain and reports none now.
- SNMP walks stop after 20000 rows per table.
- New SQLite database files are created with mode 0600. Event and schedule names are limited to 200
  characters in the API.
- Resolver: attendees can no longer flood the probe queue or the merge suggestions. New MAC/IP pairs
  are capped per MAC and poll, merge suggestions per MAC and hostname, the `/merges` list shows the
  newest 200 (with a note when cut), and the re-probe bookkeeping is pruned.
- Resolver: name probe answers (NetBIOS, mDNS, LLMNR) are accepted only from the probed IP, and LLMNR
  answers must carry the transaction ID of the query, so third parties cannot name a host.
- Merge suggestions: no suggestion is created for two MACs that are online at the same time (an
  identical hostname alone could provoke one), and `/merges` shows vendor and last port of both MACs.
- Hostnames from the network are normalised: invalid UTF-8, control, bidi and zero-width characters
  are dropped and long names are cut by characters, not bytes.
- Resolver: a panic in the local-neigh or re-probe loop is logged and the loop keeps running.

### Changed

- The "probably uplink" hint is much calmer: only a switch as LLDP neighbor (bridge capability) or
  more than `polling.uplink_mac_threshold` MACs (default now 32, was 10) count, and only in three
  forwarding-table reads in a row. Existing configs with `uplink_mac_threshold: 10` keep their value.

- `examples/discord-top10.py` escapes Discord markdown in names and disables all mentions.
- Background workers (pollers, resolvers, DHCP polling, planner) log a panic with its stack and keep
  running instead of ending the process.
- `/etc/lanleecher` is now mode `0751` (traversable, not listable), so the agent package can read
  its own configuration next to the server's.
- The certificate checks of the Kea client moved to a shared package (`internal/tlsclient`), which
  the agent uses too; behaviour unchanged.
- Dependencies updated: kin-openapi 0.153, chi 5.5, gosnmp 1.47, goose 3.29, modernc sqlite 1.60
  (supersedes Dependabot #2).
- GitHub Actions updated and pinned to commit SHAs (supersedes #3); golangci-lint v2.15.0 with a
  new `.golangci.yml` (standard linters, gofmt).
- The container image is based on Alpine 3.24 (supersedes #8).
- Port traffic is now also recorded in 5-minute buckets per port.
- `/api/v1/ports` reports `wan_link_id` and the role `wan`.

### Added

- **Host agent (`lanleecher-agent`)** for Linux hosts and Proxmox VE nodes, as its own package
  (deb, rpm, Arch, Alpine). It reports the bytes per MAC address of the host and of every running
  guest (the same tap/veth counters Proxmox uses for its graphs) to the new endpoint
  `POST /api/v1/agent/report`. LANLeecher uses them only to split a switch port with several MAC
  addresses between them; the switch stays authoritative, so an agent cannot add traffic or create
  clients ([ADR 0009](docs/adr/0009-agents-only-split-ports.md)). Agents are managed under
  Admin → Agents with their own token (scope `agent:report`). The agent always encrypts and checks
  the server certificate (system roots, CA file or pin); plain HTTP or an unchecked certificate need
  explicit settings. Agent-reported names and IPs are a new hostname source `agent`
  (first in the default `resolver.hostname_priority`). See [docs/agent.md](docs/agent.md).
- **Network map:** a new page "Map" shows switches and internet connections on a background image
  (PNG, JPEG or GIF), with the links between them and their live traffic. Links come from LLDP
  (chassis ID and system name), from the MAC tables as a fallback, from link aggregation
  memberships, and can be drawn by hand or hidden; internet connections (WAN links) are placed as
  nodes linked to their WAN ports. Everything is read over SNMP only (new: LLDP chassis ID and
  neighbor details, bridge base MAC, `dot3adAggPortAttachedAggID`, TP-Link `tpVlanPortLag`).
  Everybody logged in views, admins edit. Migration 00008. The code comes from ERS-Proxy. See
  [docs/map.md](docs/map.md).
- **Internet connections (WAN links):** named WAN links (for example fibre and an LTE backup) with
  optional booked bandwidth, managed under Admin → Internet connections. Ports get the new role
  "Internet access (WAN)" plus a link on the port page. Their traffic is counted on the port only,
  never attributed to a client, and shown from the internet's point of view on the event page
  (with a chart per link) and as live tiles with utilisation on the dashboard. New API endpoints
  `GET /api/v1/wan`, `GET /api/v1/events/{slug}/wan` and `GET /api/v1/wan-links`. See
  [docs/wan.md](docs/wan.md).
- **Export and import:** `lanleecher export` writes switches, ports, devices, MACs and events with
  their traffic to a gzip JSON file (never credentials, users or tokens); `lanleecher import` and
  Admin → Export/Import read it into another instance, with a preview, a backup and merging of
  known MACs. Imported switches are *archived*: no credentials, never polled, shown with a badge.
  See [docs/export-import.md](docs/export-import.md).
- **Package repository:** every release is published to the signed lan.party package repository
  https://repo.lan.party (apt, dnf, pacman, apk), so updates arrive with the normal system updates.
- **Docker Compose examples:** `compose.yaml` (named volume) is revised and `compose.bind.yaml`
  (data in `./data`, owned by UID 100 / GID 101) is new; both are tested in CI. The Docker section
  of [installation.md](docs/installation.md#docker-compose) explains when to use which, backup
  and the reverse proxy setup.

## [1.3.0] - 2026-11-07

### Added

- **Switches without a forwarding table** are recognised: after two successful but empty FDB
  reads in a row, the switch list marks the switch with "no MAC table" and the log says once that
  its traffic is counted per port but not attributed to clients.
- Grandstream GWN7721 tested (firmware 1.0.7.128): port traffic works, client attribution does not,
  because the switch offers no forwarding table over SNMP. See
  [switch-compatibility.md](docs/switch-compatibility.md). Simulated in the integration tests
  (`testdata/snmpsim/gwn7721.snmprec`).

## [1.2.0] - 2026-10-30

### Added

- **Planned events:** under Events → Planned events, an event can be entered with its first and
  last day (optionally with start and end times). LANLeecher starts it at the start and stops it at
  the end by itself, to the second, and records both in the audit log as actor `schedule`. Planned
  events can be started early, deleted, and must not overlap; the dashboard shows the next one
  while no event runs. Also available as `lanleecher event schedule|schedules|unschedule` and as
  `GET/POST /api/v1/schedules` and `DELETE /api/v1/schedules/{id}`. Dates use the server's time
  zone (`TZ`).

## [1.1.0] - 2026-10-25

### Added

- Port groups are easier to name: the port's label is now called **display name** in the port
  list, ports whose traffic goes to a port group say so (and whether they are still unnamed), and
  port groups in rankings link to their port. The name appears in rankings, on the board and in
  the API.
- **Not an uplink:** a port flagged as "probably uplink" (many MACs or an LLDP neighbor) can be
  confirmed as not an uplink, e.g. a hypervisor with many VMs. The hint and the warning banner then
  stay off until the confirmation is withdrawn with "Check again" or the port becomes an uplink.
  Available per port and for a selection of ports; recorded in the audit log.
- Clicking into the traffic timeline of the dashboard or an event page shows which clients caused
  the traffic in that 5-minute interval; dragging selects a longer range. The detail lists the top
  10 by total (hidden entries included and marked, as the timeline counts them too), the totals and
  average rates of the range, unassigned traffic and a link to the full ranking for the range.

### Fixed

- Pressing Enter in a field of the port list saves the rows. It used to trigger "Apply to
  selection", so a changed label or role was lost.

## [1.0.1] - 2026-10-23

### Added

- Traffic charts show a tooltip with the time and the download and upload rate of the 5-minute
  interval under the mouse pointer (or finger).

### Fixed

- Forwarding-table source `auto` falls back to BRIDGE-MIB when Q-BRIDGE reports bridge ports that
  `dot1dBasePortIfIndex` does not list. TP-Link JetStream switches (e.g. T1500G-8T) report port
  numbers in `dot1qTpFdbPort`, so their FDB entries were dropped and no traffic was attributed to
  clients. The log now warns when FDB entries point to unknown interfaces.

## [1.0.0] - 2026-10-22

### Added

- Traffic accounting per client (MAC) and event via SNMP v2c/v3 with 64-bit counters: deltas only,
  with detection of switch reboots, counter resets and 64-bit wraps, and a plausibility check
  against the link speed.
- Attribution of port traffic to a single MAC, a port group (several clients on one port) or
  "unassigned"; uplinks are counted on the port only. Port roles `client`, `multi-client`, `server`
  and `uplink`.
- Forwarding-table sources `auto`, `qbridge`, `bridge` and `cisco-vlan`; LLDP neighbors and MAC
  counts as "probably uplink" hints. Interface classification by name for switches that report
  misleading `ifType` values (tested with TP-Link Omada/JetStream).
- IP and hostname discovery with history: SNMP ARP tables, the host's neighbor table, NetBIOS,
  mDNS, LLMNR and reverse DNS. Merge suggestions for MACs with the same hostname in an event.
- Events: start/stop, rankings per event and across all events, time ranges, comparison of up to
  three events, CSV and JSON export.
- Hiding ports and devices from rankings, evaluated at query time and retroactive.
- Web UI in English and German: dashboard with live throughput, rankings, search, device pages with
  per-event history and charts, switch and port management with connection test, merge
  suggestions, users, API tokens, audit log.
- Beamer view `/board` with live updates via Server-Sent Events.
- Kea DHCP as a source for IP addresses and client hostnames: Kea 2.x via the Control Agent and
  Kea 3.x via the servers' HTTP control sockets, read-only command allowlist, HTTPS with certificate
  verification during the handshake, one-click "trust this certificate" with a warning when it
  changes, optional mutual TLS. `tools/keasim` simulates Kea for trying it out.
- Vendor names for MAC addresses from the IEEE registries (MA-L, MA-M, MA-S), embedded in the
  binary and shown on device pages, port lists, search results and rankings, and as `vendor` in the
  client API. Randomized MACs get no vendor. `tools/genoui` refreshes the data.
- Light and dark theme: automatic (follows the operating system), or chosen per user with the
  toggle in the top bar or on the account page.
- REST API v1 with scoped bearer tokens (`read:rankings`, `read:clients`, `write:events`,
  `read:board`), IP allowlists, expiry, rate limits, RFC 9457 errors and an OpenAPI 3.1
  specification that tests enforce for every endpoint and error case; Prometheus metrics.
- Security: Argon2id passwords, login lockout, CSRF protection, strict Content Security Policy,
  AES-256-GCM encryption of SNMP credentials.
- CLI: `server`, `config init`, `migrate`, `backup`, `user`, `event`, `switch`, `version`.
- Packages for Debian/Ubuntu, Fedora/RHEL, Arch Linux and Alpine (amd64, arm64, armv7), a
  multi-arch Docker image, and a release workflow that installs every package in a fresh container
  before publishing.
- Documentation: installation, configuration reference and JSON Schema (generated), reverse proxy,
  LAN party guide, switch compatibility, API guide, architecture, security model, ADRs, examples,
  `llms.txt` and `llms-full.txt`.

[Unreleased]: https://github.com/lan-dot-party/LANLeecher/compare/v1.4.1...HEAD
[1.4.1]: https://github.com/lan-dot-party/LANLeecher/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/lan-dot-party/LANLeecher/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/lan-dot-party/LANLeecher/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/lan-dot-party/LANLeecher/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/lan-dot-party/LANLeecher/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/lan-dot-party/LANLeecher/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/lan-dot-party/LANLeecher/releases/tag/v1.0.0
