# Changelog

All notable changes to this project are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
