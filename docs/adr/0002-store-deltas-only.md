# 0002 – Store deltas, never raw counters as totals

**Status:** accepted

## Context

Switch counters are not reliable totals: they restart at zero on a reboot, can be reset by an admin,
wrap around, and a client may move between ports or switches during an event. A ranking based on raw
counter values would jump backwards or credit the wrong client.

## Decision

LANLeecher stores the last raw reading per port (with `sysUpTime` and
`ifCounterDiscontinuityTime`) only to compute the next delta. Every total is the sum of deltas booked
while an event runs. A reset (uptime went backwards, discontinuity time changed) credits the new raw
value; a wrap credits new + 2⁶⁴ − old; a delta above line rate × elapsed time × 1.1 is dropped.

## Consequences

- A switch reboot, counter reset or LANLeecher restart never reduces a total.
- A client keeps its total when it moves to another port: totals belong to the MAC, not to the port.
- Traffic before an event starts is never booked, and the first poll of a new port books nothing.
- At most one poll interval of traffic can be lost when a port goes down between two polls.
- The delta logic is a pure function (`accounting.Delta`) and tested table-driven.
