# 0004 – Hide ports and devices at query time

**Status:** accepted

## Context

Servers, caches and uplinks would top every ranking, but their traffic must still be counted – for
the port overview, exports and the question "how much did the LANcache deliver?". Organizers often
notice only during the event that a port should be hidden.

## Decision

"Hide in rankings" is a flag on ports and devices. It is never written into totals. Rankings join
`traffic_totals` with `ports` and `devices` and filter at query time; the port is part of the
`traffic_totals` key so that traffic on a hidden port can be filtered even for devices that moved.

## Consequences

- Hiding and unhiding apply immediately and retroactively, also to past events, without recomputing.
- `include_hidden=true` and the "Hidden" list show the full data.
- `traffic_totals` has one row per event, subject and port instead of per event and subject; rankings
  aggregate them. The index on `(event_id, bytes_down|bytes_up)` keeps this fast.
