# Internet connections (WAN links)

A **WAN link** is a named internet connection of the party: the router ports of one provider or
one line. Several links are common, for example a fibre line plus an LTE backup. LANLeecher shows
how much traffic each link carried and, if you enter the booked bandwidth, how busy it is.

## Set up

1. **Admin → Internet connections → New internet connection.** Enter a name (for example `Fiber`) and,
   optionally, the booked download and upload in Mbit/s (0 = unknown). Order and notes are free.
2. Open the switch and its port page. For every port that leads to the router or modem, set the
   role **Internet access (WAN)** and choose the internet connection next to it. Bulk selection works
   too. A WAN port always needs a link.
3. A link with ports cannot be deleted; change the ports' role first.

Operators can assign ports; only admins create, edit or delete links.

## What is counted

- **Direction:** the internet *download* is what the router sends into the switch port, that is
  `ifHCInOctets` of the WAN port. The internet *upload* is `ifHCOutOctets`. This is the opposite
  of a client port. LANLeecher keeps port traffic in the client's view and converts it in one
  place (`accounting.WANDirection`).
- Like uplinks, WAN traffic is counted **on the port only** and never attributed to a client or a
  MAC. WAN ports are hidden in the rankings by default and show their MAC count instead of a list.
- Port traffic is recorded for **every** role. If you assign WAN ports after an event, that
  event's internet traffic is still shown, from its port totals. The 5-minute series per port
  exist only from the version that introduced WAN links on, so older events have totals but no
  chart.

## Where it shows up

- **Event page:** section *Internet* with download, upload and a chart per link, and a total row
  if there is more than one link.
- **Dashboard:** while an event runs, one tile per link with the current rate and, if bandwidth
  is booked, the utilisation in percent with a bar.
- **API** (scope `read:rankings`):

```bash
# Running event, all links
curl -H "Authorization: Bearer $TOKEN" https://lanleecher.example/api/v1/wan

# A finished event, one link, with 5-minute series
curl -H "Authorization: Bearer $TOKEN" \
  "https://lanleecher.example/api/v1/events/<slug>/wan?series=true&link=Fiber"

# Configured links and their ports
curl -H "Authorization: Bearer $TOKEN" https://lanleecher.example/api/v1/wan-links
```

Abbreviated response of `/api/v1/wan`:

```json
{
  "event": {"slug": "lan-2026", "active": true},
  "total": {"internet_down_bytes": 912340000000, "internet_up_bytes": 81230000000, "down_bps": 410000000, "up_bps": 22000000},
  "links": [
    {"id": 1, "name": "Fiber", "down_mbps": 1000, "up_mbps": 500,
     "internet_down_bytes": 912340000000, "internet_up_bytes": 81230000000,
     "down_bps": 410000000, "up_bps": 22000000, "util_down": 0.41, "util_up": 0.044,
     "ports": []}
  ]
}
```

`util_down` and `util_up` are shares of the booked bandwidth (0 to 1, can exceed 1 briefly); they
are `null` when no bandwidth is booked. `down_bps` and `up_bps` are `null` unless the event is
running. `series` (only with `series=true`) holds `{start, internet_down_bytes, internet_up_bytes}`
buckets. `/api/v1/ports` reports the role `wan` and `wan_link_id` per port. The full schema is in
the [OpenAPI specification](../internal/api/openapi.yaml).
