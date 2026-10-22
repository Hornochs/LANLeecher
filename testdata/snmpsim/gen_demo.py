#!/usr/bin/env python3
"""Generates demo.snmprec, a 24-port switch with made-up guests for screenshots.

Run: python3 testdata/snmpsim/gen_demo.py > testdata/snmpsim/demo.snmprec

All MAC addresses are locally administered (02:...), all names are invented.
Counters grow at fixed rates in octets per second (snmpsim "numeric" module).
"""

# Port 1-20: one guest each (out = client download, in = client upload).
GUESTS = [
    (9_500_000, 450_000), (7_200_000, 2_100_000), (11_800_000, 300_000), (4_100_000, 3_900_000),
    (6_600_000, 800_000), (2_900_000, 150_000), (8_400_000, 1_200_000), (1_300_000, 2_800_000),
    (5_500_000, 600_000), (3_700_000, 350_000), (10_200_000, 900_000), (650_000, 90_000),
    (4_800_000, 1_700_000), (2_200_000, 260_000), (7_900_000, 520_000), (1_900_000, 1_100_000),
    (3_100_000, 210_000), (950_000, 75_000), (6_100_000, 2_400_000), (2_600_000, 330_000),
]

rows = []


def add(oid, tag, value):
    rows.append((tuple(int(x) for x in oid.split(".")), f"{oid}|{tag}|{value}"))


def port(idx, name, alias, up, speed, rin, rout):
    add(f"1.3.6.1.2.1.2.2.1.2.{idx}", "4", name)
    add(f"1.3.6.1.2.1.2.2.1.3.{idx}", "2", 6)
    add(f"1.3.6.1.2.1.2.2.1.8.{idx}", "2", 1 if up else 2)
    add(f"1.3.6.1.2.1.31.1.1.1.1.{idx}", "4", name)
    add(f"1.3.6.1.2.1.31.1.1.1.6.{idx}", "70:numeric", f"rate={rin},initial=1000000")
    add(f"1.3.6.1.2.1.31.1.1.1.10.{idx}", "70:numeric", f"rate={rout},initial=1000000")
    add(f"1.3.6.1.2.1.31.1.1.1.15.{idx}", "66", speed)
    add(f"1.3.6.1.2.1.31.1.1.1.18.{idx}", "4", alias)
    add(f"1.3.6.1.2.1.31.1.1.1.19.{idx}", "67", 0)
    add(f"1.3.6.1.2.1.17.1.4.1.2.{idx}", "2", idx)


def fdb(mac, idx):
    octets = ".".join(str(int(b, 16)) for b in mac.split(":"))
    add(f"1.3.6.1.2.1.17.7.1.2.2.1.2.1.{octets}", "2", idx)
    add(f"1.3.6.1.2.1.17.7.1.2.2.1.3.1.{octets}", "2", 3)


add("1.3.6.1.2.1.1.1.0", "4", "Demo 24-Port Gigabit Switch")
add("1.3.6.1.2.1.1.3.0", "67:numeric", "rate=100,initial=100000")
add("1.3.6.1.2.1.1.5.0", "4", "demo-sw1")

for i, (rout, rin) in enumerate(GUESTS, start=1):
    port(i, f"Gi1/0/{i}", f"Table {i}", True, 1000, rin, rout)
    fdb(f"02:00:00:00:01:{i:02x}", i)
port(21, "Gi1/0/21", "LANcache", True, 10000, 38_000_000, 900_000)
fdb("02:00:00:00:02:01", 21)
port(22, "Gi1/0/22", "Table 21 small switch", True, 1000, 1_400_000, 5_200_000)
for i in range(1, 4):
    fdb(f"02:00:00:00:03:{i:02x}", 22)
port(23, "Gi1/0/23", "", False, 0, 0, 0)
port(24, "Port-channel1", "Uplink core", True, 2000, 60_000_000, 60_000_000)
for i in range(30):
    fdb(f"02:00:00:00:04:{i:02x}", 24)

for _, line in sorted(rows):
    print(line)
