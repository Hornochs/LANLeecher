# Network map

The map (**Map** in the top bar) shows your switches and internet connections on a background
image, the links between them, and how busy each link is right now. Everybody who is logged in can
look at it; only admins change it. The code comes from
[ERS-Proxy](https://github.com/lan-dot-party/ers-proxy), adapted to LANLeecher's switches.

## Only SNMP

Like everything else in LANLeecher, the map gets its data over SNMP only, through the connection
the poller already has. Nothing is read from a switch's web interface, REST API or SSH, and there
are no extra logins. If a switch does not offer LLDP, a bridge MAC or a forwarding table over SNMP,
the map simply finds fewer links for it. Links can always be added by hand.

## How links are found

Each poll of the forwarding table (FDB) also reads, best effort and without ever failing the poll:

- the switch's own **LLDP chassis ID** (`lldpLocChassisId`) and **bridge base MAC**
  (`dot1dBaseBridgeAddress`),
- the **LLDP neighbors** per port (chassis ID, system name, port ID, port description, bridge
  capability); if a port has several neighbors, the one that is a bridge is preferred,
- the **link aggregation** (LAG) memberships: `dot3adAggPortAttachedAggID`, and for TP-Link
  switches without it `tpVlanPortLag` ("LAG1" is matched to the LAG interface whose name ends in 1).

The map then links switches in this order:

1. **LLDP.** A neighbor whose chassis ID is the chassis ID or base MAC of a known switch (else whose
   system name is the name or sysName of one) gives a link. The far port is the one the neighbor
   names as its port ID or description, or else the port on the far switch that sees this switch.
2. **MAC tables (FDB).** For pairs of switches without an LLDP link: if the MAC of switch X is
   learned on port P of switch A, they are linked on P, unless X lies behind another switch on P.
   The far end is found the same way from X's table. These links are drawn dashed.
3. **By hand.** Admins can draw a link between two switches and pick the ports, which is the way
   to connect switches that offer neither LLDP nor a usable MAC table.

Automatic links can be **hidden** (and shown again) in edit mode. Ports in a **LAG** are shown as
one link on the LAG interface; with several cables there is still one line. A switch that is only
known from an import (archived) can be placed on a map: it is never polled, so it has no MAC table,
but the LLDP data stored before the import still counts.

Links are worked out among *all* switches, not only those on the map; only links between switches on the map are drawn.

## Internet connections

Each internet connection (WAN link, see [wan.md](wan.md)) can be placed on the map as a cloud with
its name. It is linked automatically to each of its WAN ports whose switch is on the map; these
links cannot be edited or hidden. Their rates are from the internet's point of view: the arrow
towards the switch is the internet download. A click opens Admin → Internet connections (admins
only).

## Traffic colours

The rates are the live rates of the last poll of the port at one end of the link (the other end if
that one has none). The colour shows the higher of the two directions as a share of the link speed:

| Share | Colour |
| --- | --- |
| below 1 % | green |
| below 10 % | light green |
| below 25 % | yellow |
| below 50 % | orange |
| below 75 % | dark orange |
| 75 % and more | red |

A grey line has no rates; a red dashed line is a link whose port is down or whose switch cannot be
polled. For internet connections the share is that of the booked bandwidth of the connection when
one is entered (the sum of all its ports against the download or upload bandwidth), otherwise the
port speed. Hover a link for the exact rates in both directions.

Switch nodes have a green border when the last poll worked, red when it failed, a dashed grey
border when the switch is archived (imported, not polled).

## Roles

| Role | May |
| --- | --- |
| viewer, operator | view maps; click a switch to open its page |
| admin | create, rename and delete maps, upload the background, move and add nodes, draw and hide links; open Admin → Internet connections from a WAN node |

Changes are written to the audit log as `map.*`.

## Background image

PNG, JPEG or GIF, at most 8 MiB and 50 megapixels. The type is checked by decoding the file, not by
its name; SVG is refused because it can carry script. The image is served with
`X-Content-Type-Options: nosniff` and `Content-Security-Policy: default-src 'none'; sandbox`. Its
size sets the map's coordinate space; nodes are stored as fractions of it and keep their relative
position when the image is replaced. Without an image the map is an empty 1600 × 900 area.
