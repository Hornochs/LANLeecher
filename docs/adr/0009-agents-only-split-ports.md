# 0009 – Host agents only split switch ports

**Status:** accepted

## Context

The forwarding table tells which MAC addresses sit behind a switch port, but the port counters only
give the total. Hypervisor ports (Proxmox VE with many guests), access points and small switches
therefore end up as one port group. The hosts themselves know the bytes per MAC (Proxmox shows them
per guest), so an agent on the host can report them. Agents run on machines that LANLeecher does not
control; a modified agent could report anything.

## Decision

- The agent sends absolute counters per MAC (already in the MAC's point of view) over HTTPS to
  `POST /api/v1/agent/report`, authenticated by its own token with only the scope `agent:report`.
  LANLeecher computes the deltas (counter restarts count from zero) in memory.
- The switch stays authoritative: when the poller books a delta of a port with several MACs, it takes
  the pending agent deltas of exactly the MACs the forwarding table shows on that port and splits the
  port delta with them (`accounting.Split`). If the agents report less than the port, every MAC gets
  its own value and the rest stays with the port group; if they report more, the port delta is split
  proportionally. Shares plus rest always equal the port delta.
- Agents never create MACs or devices and never add traffic. A MAC reported by two agents at once is
  ignored. IP addresses and names are recorded (source `agent`) for known MACs only, without
  link-local, loopback or multicast addresses.
- The agent verifies the server certificate by default (system roots, optional CA file or pin);
  plain HTTP and unverified TLS need explicit settings and are logged as warnings.

## Consequences

- A manipulated agent can only shift traffic between the MACs of its own port, never raise anyone's
  total or invent clients.
- Traffic between guests of one host never reaches the switch and is not counted, as before.
- Agent state is not persisted: after a restart of LANLeecher the first report only sets the
  starting point; deltas that no port poll takes within three poll intervals expire.
