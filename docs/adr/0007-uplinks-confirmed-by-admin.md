# 0007 – Uplinks are suggested, never set automatically

**Status:** accepted

## Context

An unmarked uplink puts the traffic of everything behind it on one port group and skews all
rankings. Heuristics can detect most uplinks – many MACs on a port, an LLDP neighbor, a port channel –
but they are wrong in both directions: an access point or a small switch under a table has many MACs
and an LLDP neighbor, yet is a client port; an uplink to a switch without LLDP may carry few MACs.

## Decision

LANLeecher sets the role `uplink` automatically only when a port is first discovered and is clearly
not a guest port: link aggregations and virtual interfaces. Everything else that looks like an
uplink is marked "probably uplink" with a warning; an operator decides. Once set, a role is never
changed by LANLeecher.

## Consequences

- The admin's decision is never overwritten by a poll.
- The port pages show a prominent warning until all suspected uplinks are reviewed.
- Vendors that report misleading interface types (e.g. TP-Link: `ifType` 6 for everything) need
  name-based classification, which is extended as new switches are tested.
