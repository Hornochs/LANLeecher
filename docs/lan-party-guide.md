# LAN party guide

A checklist for organizers: what to do before, during and after the event.

## Weeks before

- [ ] Check that your switches qualify: SNMP v2c or v3, 64-bit counters, forwarding table
      ([switch-compatibility.md](switch-compatibility.md)). Enable SNMP read access for the
      LANLeecher host only, with a dedicated community or v3 user.
- [ ] Install LANLeecher ([installation.md](installation.md)) on a machine that reaches the
      switches' management addresses and, for hostnames, the client network.
- [ ] Create accounts: one `admin`, `operator`s for the orga team, `viewer`s for everyone else.
- [ ] Decide whether the beamer view should be public on the LAN (`board.enabled`, `board.public`).

## Setup day

- [ ] Add every switch under **Switches → Add switch** and press **Test connection**. Check that all
      ports show "64 bit ✓".
- [ ] **Mark the uplinks.** Open every switch and set the role **uplink** on all ports to other
      switches, the router and the internet line. Ports marked "probably uplink" (LLDP neighbor or
      many MACs) are the first candidates; port channels and VLAN interfaces start as uplinks.
      An unmarked uplink puts the traffic of a whole switch on one ranking entry.
- [ ] Set the role **server** on game servers, the LANcache and file servers (hidden in rankings,
      still counted). If such a machine sits on a normal table port, hide the **device** instead –
      that sticks even if it moves.
- [ ] Set **multi-client** on ports with a small switch under the table and give them a **label**
      ("Table 7"); they appear as one entry.
- [ ] Give table ports labels if you like – they show up in port lists and for port groups.
- [ ] Ask Windows guests to set the LAN network to **Private**: in the "Public" profile the Windows
      firewall blocks NetBIOS and mDNS, and only the MAC remains as a name.
- [ ] Create an API token with scope `read:board` for an OBS overlay or Discord bot
      ([examples/](../examples/)).

## Event start

- [ ] **Events → Start event** with a name ("Basement LAN #12") – or `lanleecher event start "…"`.
      Counting starts with the next poll; traffic before the start is not booked.
- [ ] Open `/board` on the beamer (`?limit=20` or `?limit=50` for longer lists).
- [ ] Watch the dashboard: all switches green, throughput plausible.

## During the event

- [ ] If a port suddenly tops the ranking with many MACs, it is probably an unmarked uplink: set the
      role. Past traffic stays on the port group; hide that group if needed – hiding works
      retroactively.
- [ ] Review **Merges** now and then: a guest who changed network cards shows up under the same
      hostname with a new MAC. Merge only when you are sure; names like `PC` are not unique.
- [ ] Use **Search** (MAC, IP or hostname) to answer "who is 10.0.3.57?".
- [ ] A switch reboot or a LANLeecher restart loses nothing: counters are continued, resets are
      detected.

## After the event

- [ ] **Stop the event.** Totals are frozen, all data stays.
- [ ] Export the results: **Events → <event> → CSV / JSON**.
- [ ] Take a backup: `sudo -u lanleecher lanleecher backup`, and keep `secret.key` with it.
- [ ] Compare with earlier LANs under **Events → Compare events**, or ask the API for the ranking
      across all events (`?event=all`).

## Privacy

LANLeecher stores MAC addresses, IP addresses and hostnames of your guests with timestamps, and their
traffic volume per event – no content, no destinations. Tell your guests, keep the web UI behind a
login, give `read:clients` only to tokens that need it, and delete old events' data if your privacy
policy requires it (stop the service and remove the database, or ask for a retention feature).
