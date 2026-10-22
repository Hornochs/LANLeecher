# 0003 – No SNMP v1, 64-bit counters only

**Status:** accepted

## Context

SNMP v1 only has 32-bit counters (`ifInOctets`/`ifOutOctets`). At 1 Gbit/s a 32-bit octet counter
wraps after about 34 seconds; with a 30-second poll interval and some jitter, wraps cannot be told
apart from multiple wraps, and the numbers would be silently wrong.

## Decision

LANLeecher supports SNMP v2c and v3 and uses only `ifHCInOctets`/`ifHCOutOctets`. Ports without
64-bit counters are shown as "unsupported" and not counted.

## Consequences

- Very old switches cannot be used. Every managed switch of the last 15 years offers v2c.
- No 32-bit wrap heuristics in the code; the plausibility check catches remaining anomalies.
