#!/usr/bin/env python3
"""Generates testswitch.snmprec, a small simulated switch for integration tests.

Run: python3 testdata/snmpsim/gen.py > testdata/snmpsim/testswitch.snmprec

Counters use snmpsim's "numeric" variation module and grow at a fixed rate
(octets per second since the simulator started), so two polls a few seconds
apart produce predictable deltas. ifHCOutOctets (client download) grows ten
times faster than ifHCInOctets (client upload) to catch a swapped mapping.
"""

# ifIndex: (ifName, ifAlias, operUp, speed Mbit/s, in rate, out rate)
PORTS = {
    1: ("gigabitEthernet 1/0/1", "Table 1", True, 1000, 100_000, 1_000_000),
    2: ("gigabitEthernet 1/0/2", "Table 2 small switch", True, 1000, 50_000, 500_000),
    3: ("gigabitEthernet 1/0/3", "", False, 0, 0, 0),
    4: ("port-channel 1", "Uplink Core", True, 2000, 3_000_000, 3_000_000),
    100: ("Vlan-interface100", "", True, 0, 10_000, 10_000),
}

# MAC -> ifIndex (VLAN 1)
FDB = {
    "02:00:00:00:00:01": 1,
    "02:00:00:00:00:21": 2,
    "02:00:00:00:00:22": 2,
}
FDB.update({f"02:00:00:00:10:{i:02x}": 4 for i in range(12)})

rows = []


def add(oid, tag, value):
    rows.append((tuple(int(x) for x in oid.split(".")), f"{oid}|{tag}|{value}"))


add("1.3.6.1.2.1.1.1.0", "4", "LANLeecher Test Switch")
add("1.3.6.1.2.1.1.3.0", "67:numeric", "rate=100,initial=100000")
add("1.3.6.1.2.1.1.5.0", "4", "testswitch")

for idx, (name, alias, up, speed, rin, rout) in PORTS.items():
    add(f"1.3.6.1.2.1.2.2.1.2.{idx}", "4", name)
    add(f"1.3.6.1.2.1.2.2.1.3.{idx}", "2", 6)  # ethernetCsmacd for all, like TP-Link
    add(f"1.3.6.1.2.1.2.2.1.8.{idx}", "2", 1 if up else 2)
    add(f"1.3.6.1.2.1.31.1.1.1.1.{idx}", "4", name)
    add(f"1.3.6.1.2.1.31.1.1.1.6.{idx}", "70:numeric", f"rate={rin},initial=1000000")
    add(f"1.3.6.1.2.1.31.1.1.1.10.{idx}", "70:numeric", f"rate={rout},initial=1000000")
    add(f"1.3.6.1.2.1.31.1.1.1.15.{idx}", "66", speed)
    add(f"1.3.6.1.2.1.31.1.1.1.18.{idx}", "4", alias)
    add(f"1.3.6.1.2.1.31.1.1.1.19.{idx}", "67", 0)
    add(f"1.3.6.1.2.1.17.1.4.1.2.{idx}", "2", idx)

for mac, port in FDB.items():
    octets = ".".join(str(int(b, 16)) for b in mac.split(":"))
    add(f"1.3.6.1.2.1.17.7.1.2.2.1.2.1.{octets}", "2", port)
    add(f"1.3.6.1.2.1.17.7.1.2.2.1.3.1.{octets}", "2", 3)

# ARP table (ipNetToMediaPhysAddress) for the snmp-arp resolver.
for i, mac in enumerate(["02:00:00:00:00:01", "02:00:00:00:00:21"], start=11):
    add(f"1.3.6.1.2.1.4.22.1.2.100.10.0.0.{i}", "4x", mac.replace(":", ""))

for _, line in sorted(rows):
    print(line)
