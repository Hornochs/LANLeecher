# 0005 – SQLite only, no PostgreSQL

**Status:** accepted (supersedes the original plan of an optional PostgreSQL backend)

## Context

The original plan offered PostgreSQL as an optional backend with a migration command from SQLite.
The write load of LANLeecher is small: one transaction per switch and poll interval, a few hundred rows
each, even at a large LAN party. A second backend would double the SQL to maintain and test, and add
a database server to installations that should work offline on a small box.

## Decision

LANLeecher uses SQLite only (`modernc.org/sqlite`, WAL mode, `busy_timeout`). There is no
`storage.type` setting and no PostgreSQL code.

## Consequences

- Installation needs no database server; a backup is one file (`lanleecher backup` uses
  `VACUUM INTO` and is consistent while running).
- Only one SQL dialect to write and test.
- One LANLeecher instance per database; no shared database for several instances. If that is ever
  needed, a new record would revisit this decision.
