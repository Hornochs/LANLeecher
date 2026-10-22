#!/usr/bin/env python3
"""Seeds made-up traffic history into a LANLeecher demo database, for screenshots.

Run it against a demo instance that polls testdata/snmpsim/demo.snmprec (see
CONTRIBUTING.md), after the first forwarding-table read, while the server runs:

    python3 testdata/demo/seed_history.py data/lanleecher.db

It writes directly into SQLite (schema version 2) and only touches the demo switch:
  - the running event gets six hours of 5-minute buckets before the current one:
    guests arriving, a dinner dip, a big game patch served by the LANcache, guests
    going to sleep, a streamer uploading all evening;
  - two earlier LAN parties with a subset of the guests.
Totals are the sums of the buckets, so rankings, timelines and device history agree.
All data is invented; the random generator is seeded, so the result is reproducible.
"""
import math
import random
import sqlite3
import sys
import time

BUCKET = 300  # seconds
MBIT = 1e6 / 8  # bytes per second at 1 Mbit/s

# Per guest (port 1-20): typical download / upload in Mbit/s and a profile.
GUESTS = [
    (60, 4, "gamer"), (45, 12, "gamer"), (80, 3, "downloader"), (35, 30, "seeder"), (55, 6, "gamer"),
    (25, 2, "casual"), (70, 9, "gamer"), (15, 22, "streamer"), (40, 4, "casual"), (30, 3, "gamer"),
    (75, 7, "downloader"), (8, 1, "retro"), (35, 14, "voice"), (20, 2, "casual"), (65, 5, "gamer"),
    (18, 9, "casual"), (28, 2, "sleeper"), (10, 1, "sleeper"), (50, 18, "streamer"), (25, 3, "gamer"),
]


def activity(h: float) -> float:
    """Overall activity over the evening (h = hours since the event started)."""
    a = 0.75 + 0.25 * math.sin(h * 1.3)
    if 2.4 <= h <= 3.1:  # dinner
        a *= 0.35
    if h > 5.2:  # late night
        a *= 0.7
    return a


def guest_rates(rng, i, profile, base_down, base_up, h, arrival, patch, sleep_at):
    if h < arrival or h > sleep_at:
        return 0.0, 0.0
    noise = rng.lognormvariate(0, 0.35)
    down = base_down * activity(h) * noise
    up = base_up * activity(h) * rng.lognormvariate(0, 0.3)
    if profile == "streamer":
        up = base_up * rng.uniform(0.9, 1.1)  # constant upload while streaming
    if profile == "downloader" and rng.random() < 0.08:
        down += rng.uniform(300, 700)  # occasional big download
    if patch[0] <= h <= patch[1] and i in patch[2]:
        down += rng.uniform(220, 420)  # game patch from the LANcache
    return down, up


def seed_event(db, event_id, start, end, guests, rng, ports, macs, group_id, now_bucket):
    totals = {}  # (subject_type, subject_id, port_id) -> [down, up]
    port_totals = {}

    def add(subject_type, subject_id, port_id, bucket, down_bps, up_bps):
        d, u = int(down_bps * MBIT * BUCKET), int(up_bps * MBIT * BUCKET)
        if d == 0 and u == 0:
            return
        db.execute("""INSERT INTO traffic_buckets (event_id, subject_type, subject_id, port_id, bucket_start, bytes_down, bytes_up)
                      VALUES (?,?,?,?,?,?,?) ON CONFLICT (event_id, subject_type, subject_id, port_id, bucket_start)
                      DO UPDATE SET bytes_down = bytes_down + excluded.bytes_down, bytes_up = bytes_up + excluded.bytes_up""",
                   (event_id, subject_type, subject_id, port_id, bucket, d, u))
        t = totals.setdefault((subject_type, subject_id, port_id), [0, 0])
        t[0] += d
        t[1] += u
        p = port_totals.setdefault(port_id, [0, 0])
        p[0] += d
        p[1] += u

    hours = (end - start) / 3600
    arrival = {i: rng.uniform(0, 1.5) for i in guests}
    sleep_at = {i: (rng.uniform(4.6, 5.6) if GUESTS[i - 1][2] == "sleeper" else hours + 1) for i in guests}
    patch_guests = set(rng.sample(sorted(guests), k=min(12, len(guests))))
    patch = (hours * 0.6, hours * 0.6 + 0.6, patch_guests)

    bucket = start - start % BUCKET
    while bucket < min(end, now_bucket):
        h = (bucket - start) / 3600
        cache_up = 0.0
        for i in guests:
            base_down, base_up, profile = GUESTS[i - 1]
            down, up = guest_rates(rng, i, profile, base_down, base_up, h, arrival[i], patch, sleep_at[i])
            add("mac", macs[i], ports[i], bucket, down, up)
            if patch[0] <= h <= patch[1] and i in patch_guests:
                cache_up += down * 0.9
        # Table 21: three guests behind a small switch.
        add("port_group", str(group_id), ports[22], bucket, 70 * activity(h) * rng.lognormvariate(0, 0.3),
            12 * activity(h) * rng.lognormvariate(0, 0.3))
        # LANcache: delivers the patch, little else.
        add("mac", macs[21], ports[21], bucket, 5 * rng.random(), cache_up + 20 * activity(h))
        bucket += BUCKET

    for (subject_type, subject_id, port_id), (d, u) in totals.items():
        db.execute("""INSERT INTO traffic_totals (event_id, subject_type, subject_id, port_id, bytes_down, bytes_up, updated_at)
                      VALUES (?,?,?,?,?,?,?) ON CONFLICT (event_id, subject_type, subject_id, port_id)
                      DO UPDATE SET bytes_down = bytes_down + excluded.bytes_down, bytes_up = bytes_up + excluded.bytes_up""",
                   (event_id, subject_type, subject_id, port_id, d, u, end))
    # The uplink carries the internet share of the guests' traffic.
    all_down = sum(v[0] for k, v in port_totals.items())
    all_up = sum(v[1] for k, v in port_totals.items())
    port_totals[ports[24]] = [int(all_down * 0.35), int(all_up * 0.25)]
    for port_id, (d, u) in port_totals.items():
        db.execute("""INSERT INTO port_traffic_totals (event_id, port_id, bytes_down, bytes_up, updated_at) VALUES (?,?,?,?,?)
                      ON CONFLICT (event_id, port_id) DO UPDATE SET bytes_down = bytes_down + excluded.bytes_down,
                      bytes_up = bytes_up + excluded.bytes_up""", (event_id, port_id, d, u, end))


def main():
    db = sqlite3.connect(sys.argv[1], timeout=30)
    rng = random.Random(12)
    now = int(time.time())
    now_bucket = now - now % BUCKET

    ports = {row[0]: row[1] for row in db.execute(
        "SELECT p.if_index, p.id FROM ports p JOIN switches s ON s.id = p.switch_id WHERE s.name = 'demo-sw1'")}
    if len(ports) < 24:
        sys.exit("demo switch not polled yet – wait for the first poll")
    macs = {i: f"02:00:00:00:01:{i:02x}" for i in range(1, 21)}
    macs[21] = "02:00:00:00:02:01"
    for mac in macs.values():
        if not db.execute("SELECT 1 FROM macs WHERE mac = ?", (mac,)).fetchone():
            sys.exit("forwarding table not read yet – wait a few seconds")

    def group(event_id):
        db.execute("INSERT OR IGNORE INTO port_groups (event_id, port_id) VALUES (?, ?)", (event_id, ports[22]))
        gid = db.execute("SELECT id FROM port_groups WHERE event_id = ? AND port_id = ?", (event_id, ports[22])).fetchone()[0]
        for k in range(1, 4):
            db.execute("INSERT OR IGNORE INTO port_group_macs VALUES (?, ?, ?, ?)", (gid, f"02:00:00:00:03:{k:02x}", now, now))
        return gid

    # Earlier LAN parties: ended, a subset of the guests.
    for number, days_ago, k in ((10, 63, 13), (11, 28, 16)):
        day = time.localtime(now - days_ago * 86400)
        start = int(time.mktime((day.tm_year, day.tm_mon, day.tm_mday, 18, 0, 0, 0, 0, -1)))  # 18:00 local
        end = start + 8 * 3600
        cur = db.execute("INSERT OR IGNORE INTO events (name, slug, started_at, ended_at, notes) VALUES (?,?,?,?,'')",
                         (f"Demo LAN #{number}", f"demo-lan-{number}", start, end))
        if cur.rowcount == 0:
            continue  # already seeded
        event_id = cur.lastrowid
        guests = set(rng.sample(range(1, 21), k=k)) | {3, 11}
        seed_event(db, event_id, start, end, guests, rng, ports, macs, group(event_id), end)

    # The running event: six hours of history before the current bucket.
    event_id, started = db.execute("SELECT id, started_at FROM events WHERE ended_at IS NULL").fetchone()
    start = now_bucket - 6 * 3600
    if started > start:
        db.execute("UPDATE events SET started_at = ? WHERE id = ?", (start, event_id))
        seed_event(db, event_id, start, now_bucket, set(range(1, 21)), rng, ports, macs, group(event_id), now_bucket)
    # Guests were first seen at their first event, not when the demo started.
    for (mac,) in db.execute("SELECT mac FROM macs WHERE mac LIKE '02:00:00:00:%'").fetchall():
        row = db.execute("""SELECT MIN(e.started_at) FROM traffic_totals t JOIN events e ON e.id = t.event_id
                            WHERE t.subject_type = 'mac' AND t.subject_id = ?""", (mac,)).fetchone()
        first = row[0] if row and row[0] else None
        if first is None:
            continue
        for table in ("macs", "mac_locations", "mac_ip_history", "mac_hostname_history"):
            db.execute(f"UPDATE {table} SET first_seen = MIN(first_seen, ?) WHERE mac = ?", (first + 600, mac))
    db.commit()
    print("seeded")


if __name__ == "__main__":
    main()
