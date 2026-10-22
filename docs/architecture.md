# Architecture

LANLeecher is one Go process: a poller per switch, a resolver service, and an HTTP server for the
web UI and the API, all sharing one SQLite database.

```
            ┌─────────────┐  SNMP (UDP 161)   ┌──────────────────────────────────────────────┐
 switches ◀─┤  scheduler  ├──────────────────▶│ collector: IF-MIB counters, names, status    │
            │ (1 loop per │                   │ fdb:       MAC → port, LLDP neighbors          │
            │   switch)   │                   └──────────────────────────────────────────────┘
            └──────┬──────┘
                   │ readings
                   ▼
            ┌─────────────┐   deltas, targets   ┌──────────┐   queries   ┌──────────────────┐
            │ accounting  ├────────────────────▶│  store   │◀────────────┤ web UI, API,     │
            │ (pure code) │                     │ (SQLite) │             │ board, metrics   │
            └─────────────┘                     └────▲─────┘             └──────────────────┘
                                                     │ IPs, hostnames
                                              ┌──────┴──────┐  ARP, NetBIOS, mDNS, LLMNR, DNS
                                              │  resolver   ├──────────────────────────────▶ clients
                                              └─────────────┘
```

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/lanleecher` | CLI (cobra); `server` wires everything together |
| `internal/config` | loads and validates `config.yaml` |
| `internal/secret` | AES-256-GCM for credentials, key in `secret.key` |
| `internal/snmp` | gosnmp connection setup (v2c/v3), walk helpers |
| `internal/collector` | reads IF-MIB counters; classifies interfaces (physical, LAG, virtual) |
| `internal/fdb` | forwarding-table sources (`auto`, `qbridge`, `bridge`, `cisco-vlan`) and LLDP |
| `internal/accounting` | `Delta` (reset, wrap, plausibility), `Attribute` (who gets the traffic), `ClientDirection` |
| `internal/scheduler` | poll loop per switch, booking into the active event, live rates |
| `internal/resolver` | address sources and name probes, hostname history, merge suggestions |
| `internal/store` | SQLite access, embedded goose migrations, rankings, search |
| `internal/board` | board payload shared by `/board` and `/api/v1/board`, rank changes, SSE fan-out |
| `internal/auth` | Argon2id, tokens, login lockout, rate limiter |
| `internal/app` | shared services and request helpers (sessions, client IP, audit, security headers) |
| `internal/api` | REST API v1, OpenAPI document, API docs page |
| `internal/web` | server-rendered UI: templates, htmx, i18n, SVG charts |
| `tools/gendocs` | generates the configuration reference, the config JSON Schema and `llms-full.txt` |

## A poll cycle

Every `polling.interval` (per switch, default 30 s):

1. **Counters:** connect, read `sysUpTime`, walk the IF-MIB columns.
2. **Ports:** upsert all interfaces. New ports get a default role (`client`; `uplink` for LAGs and
   virtual interfaces) – existing ports keep the admin's settings.
3. **Deltas:** for every up port with 64-bit counters, `accounting.Delta` compares with the stored
   reading (`port_counter_state`) for both directions. The new reading is stored in the same
   transaction.
4. **Booking** (only while an event runs): the port total is increased; `accounting.Attribute`
   decides the target – a MAC, the port group, "unassigned" or nobody (uplink) – using the MACs of
   the last FDB read. Totals and 5-minute buckets are updated.
5. **FDB** (every `polling.fdb_interval`): read the forwarding table and LLDP neighbors, store MAC
   locations (not on uplinks), update MAC counts and the "probably uplink" hint; read the ARP table if
   the switch is an ARP source.
6. **Notify:** the board service wakes stream subscribers.

All writes of one cycle run in one transaction. A failed poll writes nothing but the switch status.

## Data model

See the tables in [PROJECT_PLAN.md](../PROJECT_PLAN.md#data-model). Key points:

- Timestamps are Unix seconds (UTC); byte counts are 64-bit integers; raw counters are stored as
  `int64` with the same bit pattern as the `uint64` value.
- `traffic_totals` and `traffic_buckets` are keyed by event, subject **and port**, so visibility can
  be filtered per port at query time.
- A ranking entry of type `mac` is a device: the query sums the MACs of each device.

## Web UI

Server-rendered `html/template` pages with htmx for the live parts (dashboard refresh, connection
test). No build step, no Node.js; htmx and all assets are embedded. Charts are SVG generated on the
server. Every page set is parsed once at start; `i18n.go` holds the English and German strings.

## Security

- Sessions: random 256-bit cookie value, stored as SHA-256; `HttpOnly`, `SameSite=Lax`, `Secure`
  with an https `public_url`; 12-hour sliding lifetime. Every POST carries a per-session CSRF token
  (form field or `X-CSRF-Token`).
- Content Security Policy without inline scripts or styles; all output is escaped.
- API tokens: `llk_` + 40 base62 characters, stored as SHA-256, scoped, optional expiry and IP
  allowlist, rate-limited.
- Credentials of switches are encrypted with AES-256-GCM; the key is a separate file.

More in [security.md](security.md).

## Design decisions

The reasoning behind the main choices is recorded in [docs/adr/](adr/).
