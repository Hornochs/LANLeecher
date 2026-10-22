# Architecture decision records

Short records of decisions that shape LANLeecher, with the reasoning behind them. A record is not
changed after it is accepted; a new record supersedes it.

| # | Decision | Status |
| --- | --- | --- |
| [0001](0001-go.md) | Go instead of Python | accepted |
| [0002](0002-store-deltas-only.md) | Store deltas, never raw counters as totals | accepted |
| [0003](0003-no-snmp-v1.md) | No SNMP v1, 64-bit counters only | accepted |
| [0004](0004-hide-at-query-time.md) | Hide ports and devices at query time | accepted |
| [0005](0005-sqlite-only.md) | SQLite only, no PostgreSQL | accepted |
| [0006](0006-board-is-the-api.md) | The beamer view renders the board API | accepted |
| [0007](0007-uplinks-confirmed-by-admin.md) | Uplinks are suggested, never set automatically | accepted |

Template: context – decision – consequences.
