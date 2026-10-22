# Roadmap

The full design is in [PROJECT_PLAN.md](../PROJECT_PLAN.md). Status of the milestones:

| # | Milestone | Status |
| --- | --- | --- |
| 1 | Skeleton: CLI, config, logging, CI | done |
| 2 | Storage: SQLite, migrations | done |
| 3 | Accounting core: deltas, resets, wraps, attribution | done |
| 4 | SNMP collector v2c/v3, poll loop, connection test | done |
| 5 | FDB: Q-BRIDGE, BRIDGE, Cisco per-VLAN, LLDP uplink hints | done |
| 6 | Events: lifecycle, totals, 5-minute buckets | done |
| 7 | Auth & web UI (English/German) | done |
| 8 | REST API, board with SSE, Prometheus, OpenAPI | done |
| 9 | Resolvers & history, Kea DHCP (2.x and 3.x), MAC vendor names | done |
| 10 | Packages, Docker, release workflow | done |
| 11 | Documentation & polish | done – real-LAN test pending |

## Next

- **API response validation** in CI: every handler response checked against the OpenAPI document
  (`kin-openapi`).
- **Upgrade test** in the release workflow, from the previous release to the new one.
- **First real LAN party** and a report on the switch compatibility page.

## Ideas

- gNMI collector for switches with streaming telemetry.

Not planned: PostgreSQL (see [ADR 0005](adr/0005-sqlite-only.md)), SNMP v1
(see [ADR 0003](adr/0003-no-snmp-v1.md)).
