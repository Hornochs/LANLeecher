# Export and import

Purpose: move the recordings of a LAN party to another LANLeecher instance, for example to analyse
them elsewhere or to keep them after the original server is gone.

## What is in the file

A gzip-compressed JSON file with natural keys (no database ids):

- switches (name, host, port, SNMP version, polling settings, system info) and their ports
  (labels, roles, hiding, uplink hints)
- devices and MACs with first/last seen, locations, IP and hostname histories, port groups
- the selected events (all by default) with their traffic totals per port and subject, and the
  5-minute buckets, including the 5-minute buckets per port
- internet connections (WAN links) with their booked bandwidth and the link of each WAN port

Never in the file: credentials (communities, SNMPv3 passwords), users, sessions, API tokens, the
audit log, DHCP sources, settings and merge suggestions. The secret key of the source instance is
therefore not needed on the target.

## Command line

```bash
lanleecher export [--event slug]… [-o file]
lanleecher import [--dry-run] [--event-conflict new|skip] [--no-backup] file
```

`export` without `--event` writes all events; `-o -` writes to stdout. `import --dry-run` prints
the report and changes nothing. Before a real import the database is backed up to
`backups/lanleecher-before-import-<timestamp>.db` next to the database file (unless `--no-backup`).

## Web UI

Admin → Export/Import (admin role). Export: tick the events (none ticked = all) and download.
Import: choose the file and the event conflict policy. LANLeecher shows a preview of exactly what
would happen; nothing is imported until you confirm. The uploaded file is kept for at most 15
minutes and removed after the confirmation, the cancellation or the timeout. The import makes a
backup first and is recorded in the audit log.

## Matching rules

- **Switches:** the same name and the same host are mapped to the existing switch. Any other switch
  is created as *archived*: no credentials, disabled, never polled; edit and connection test are
  not available, deleting is. If the name is taken by a switch with another host, the imported one
  is named `<name> (import)`.
- **WAN links:** matched by name, otherwise created. A WAN port whose link is missing in the file
  is imported as an uplink.
- **MACs:** an existing MAC keeps its device. Histories are merged (earliest first seen, latest
  last seen); the display name is only filled in if it is empty. New MACs are grouped into devices
  like in the source.
- **Events:** if the slug is taken, the event is imported as `<slug>-import` or skipped
  (`--event-conflict`, or the choice in the web UI). Events are always imported as ended; an event
  that was running in the source is ended at its last traffic.

## Limits

128 MiB compressed, 1 GiB uncompressed. Larger files are rejected.

## Moving data from an older LANLeecher version

The export command is only available in newer versions. To move data from an older instance:

1. Copy its database file (for example with `lanleecher backup`) to a machine with the new version.
2. Write a minimal `config.yaml` whose `storage.path` points at the copy.
3. Run `lanleecher -c that-config.yaml export -o export.json.gz` there.

Migrations run on the copy only. The secret key of the old instance is not needed because no
credentials are exported. Then import `export.json.gz` into the target instance.
