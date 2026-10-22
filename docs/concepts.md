# Concepts

The terms LANLeecher uses in the UI, the API and the code.

## Event

A LAN party. Traffic is only booked while an event is running, and at most one event runs at a
time; starting a new event stops the running one. Without a running event, LANLeecher keeps polling
(so counters stay current) but books nothing. A stopped event keeps all its data; its totals are
frozen. Events are addressed by their **slug**, derived from the name (`Basement LAN #12` →
`basement-lan-12`).

## Switch and port

A **switch** is polled via SNMP v2c or v3. Every interface it reports becomes a **port**. Ports are
identified by switch and `ifIndex`; name (`ifName`), description (`ifAlias`), status and speed are
refreshed on every poll.

Interfaces that are not real ports (VLAN interfaces, loopbacks) are **virtual** and hidden in the
port list by default. Link aggregations (port channels) count as physical but start as uplinks.

## Port role

Each port has one role that decides who gets credited for its traffic:

| Role | Traffic goes to | In rankings by default |
| --- | --- | --- |
| `client` | the MAC learned on the port; a port group if several MACs are learned | visible |
| `multi-client` | always the port group (a small switch with several guests) | visible |
| `server` | like `client` | hidden |
| `uplink` | nobody – counted on the port only | hidden |

An unmarked uplink is the most common configuration mistake: all traffic behind it lands on one port
group and tops every ranking. LANLeecher marks a port as **probably uplink** when it has more MACs
than `polling.uplink_mac_threshold` or an LLDP neighbor; an admin confirms it by setting the role.

## MAC, device and port group

- A **MAC** address is the key of a client. MACs are normalized to `aa:bb:cc:dd:ee:ff`; the API
  accepts any notation. A MAC whose second-lowest bit of the first byte is set is *locally
  administered* and often randomized.
- A **device** groups one or more MACs that belong to one machine. Every new MAC gets its own device;
  an operator can merge devices (e.g. after a NIC change) and split a MAC off again. Rankings show
  devices, not single MACs.
![A device page: totals per event, timeline, hostname and IP history, ports](images/device.png)

- A **port group** is the shared ranking entry of a port with several clients. It lists all MACs
  seen on the port during the event.
- **Unassigned** traffic was seen on a client port while no MAC was learned there (e.g. right after
  link-up). It stays on the port and is never credited to a MAC that shows up later.

## Download and upload

Everything is seen from the client:

- `bytes_down` – data the client received = `ifHCOutOctets` of its switch port.
- `bytes_up` – data the client sent = `ifHCInOctets` of its switch port.

Throughput is reported in bit/s, totals in bytes. Byte values in the UI use decimal units
(1 GB = 10⁹ bytes).

## Deltas, resets and wraps

LANLeecher stores the last raw counter of every port and books only the **delta** between two polls.
A total is the sum of its deltas, so a switch reboot or counter reset never reduces it:

- **Reset** (sysUpTime went backwards or `ifCounterDiscontinuityTime` changed): the new raw value is
  the traffic since the reset and is credited.
- **Wrap** (smaller value without a reset signal): delta = new + 2⁶⁴ − old.
- **Plausibility check:** a delta larger than link speed × elapsed time × 1.1 is dropped and logged.
- A port that is down, or a failed poll, books nothing; the next successful poll continues from the
  last stored value.

Only 64-bit counters (SNMP v2c/v3) are used. Ports without them are marked "unsupported" and not
counted; SNMP v1 is not supported at all.

## Hiding

A port or a device can be **hidden in rankings**. It is still fully counted; hiding only removes it
from rankings, the board and ranking API responses. Visibility is evaluated at query time, so hiding
or showing an entry applies retroactively to past events. Typical candidates: LANcache, game and file
servers. `include_hidden=true` (API) or "Show hidden" (UI) brings them back, marked as hidden.

## Names: hostname, label, display name

The name of a ranking entry is, in this order:

1. the **display name** an operator set on the device,
2. the preferred **hostname** (sources ranked by `resolver.hostname_priority`),
3. the MAC address.

Port groups use the port **label**, or switch and port name. Outside of logged-in views and the
`read:clients` scope, a MAC is never shown as a name; such devices appear as `Client #<id>`.

## Resolvers

Resolvers find IP addresses and hostnames. Address sources deliver MAC/IP pairs: `snmp-arp` (ARP
table of switches marked as ARP source) and `local-neigh` (the neighbor table of the LANLeecher host).
Name probes ask an IP for its name: `netbios`, `mdns`, `llmnr`, `dns-ptr`. Every result is kept in the
**IP history** and **hostname history** with its source and first/last seen.

## Merge suggestion

When a MAC gets a hostname that another MAC already had during the running event, LANLeecher
suggests merging both devices. An operator accepts or rejects; nothing is merged automatically,
because names such as `DESKTOP-ABC123` or `PC` are not unique.

## Board

The beamer view at `/board` shows the top downloaders and uploaders of the active event with live
updates. It renders exactly what `GET /api/v1/board` returns: no MACs, no IPs, no hidden entries.
`rank_change` tells how many places an entry moved since the previous poll.

## Theme

The web UI has a light and a dark theme. **Automatic** (the default) follows the operating system or
browser; each user can pick light or dark with the toggle in the top bar or on the account page. The
choice is stored with the user and in a cookie, so the login page matches after logging out. The
beamer view is always dark.

## Roles of users

| Role | May |
| --- | --- |
| `viewer` | see rankings, search, detail pages and events |
| `operator` | additionally start/stop events, set port roles and visibility, merge devices |
| `admin` | additionally manage switches, users, API tokens; see settings and the audit log |
