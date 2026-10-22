-- +goose Up
-- All timestamps are Unix seconds (UTC), all byte counts 64-bit integers.

CREATE TABLE switches (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL UNIQUE,
    host            TEXT    NOT NULL,
    port            INTEGER NOT NULL DEFAULT 161,
    snmp_version    TEXT    NOT NULL DEFAULT 'v2c',
    credentials_enc BLOB    NOT NULL,
    poll_interval   INTEGER NOT NULL DEFAULT 0,  -- seconds, 0 = default from config.yaml
    fdb_adapter     TEXT    NOT NULL DEFAULT 'auto',
    arp_source      INTEGER NOT NULL DEFAULT 0,  -- read the ARP table for snmp-arp
    enabled         INTEGER NOT NULL DEFAULT 1,
    sys_descr       TEXT    NOT NULL DEFAULT '',
    sys_name        TEXT    NOT NULL DEFAULT '',
    last_poll_at    INTEGER,
    last_poll_ok    INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL
);

CREATE TABLE ports (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    switch_id          INTEGER NOT NULL REFERENCES switches(id) ON DELETE CASCADE,
    if_index           INTEGER NOT NULL,
    if_name            TEXT    NOT NULL DEFAULT '',
    if_alias           TEXT    NOT NULL DEFAULT '',
    if_descr           TEXT    NOT NULL DEFAULT '',
    if_type            INTEGER NOT NULL DEFAULT 0,
    physical           INTEGER NOT NULL DEFAULT 1,  -- 0 = VLAN interface, loopback, ...
    lldp_neighbor      TEXT    NOT NULL DEFAULT '',
    oper_up            INTEGER NOT NULL DEFAULT 0,
    speed_mbps         INTEGER NOT NULL DEFAULT 0,
    hc_supported       INTEGER NOT NULL DEFAULT 1,
    role               TEXT    NOT NULL DEFAULT 'client',
    label              TEXT    NOT NULL DEFAULT '',
    hidden_in_rankings INTEGER NOT NULL DEFAULT 0,
    mac_count          INTEGER NOT NULL DEFAULT 0,
    uplink_suspect     INTEGER NOT NULL DEFAULT 0,
    last_seen          INTEGER NOT NULL,
    UNIQUE (switch_id, if_index)
);

CREATE TABLE port_counter_state (
    port_id       INTEGER PRIMARY KEY REFERENCES ports(id) ON DELETE CASCADE,
    in_raw        INTEGER NOT NULL,  -- uint64 stored as int64 (same bit pattern)
    out_raw       INTEGER NOT NULL,
    counter_bits  INTEGER NOT NULL DEFAULT 64,
    sys_uptime    INTEGER NOT NULL,
    discontinuity INTEGER NOT NULL,
    polled_at     INTEGER NOT NULL
);

CREATE TABLE events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    slug       TEXT    NOT NULL UNIQUE,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER,
    notes      TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX events_started ON events (started_at);

CREATE TABLE port_traffic_totals (
    event_id   INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    port_id    INTEGER NOT NULL REFERENCES ports(id) ON DELETE CASCADE,
    bytes_down INTEGER NOT NULL DEFAULT 0,
    bytes_up   INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (event_id, port_id)
);

CREATE TABLE devices (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    display_name       TEXT    NOT NULL DEFAULT '',
    hidden_in_rankings INTEGER NOT NULL DEFAULT 0,
    notes              TEXT    NOT NULL DEFAULT '',
    created_at         INTEGER NOT NULL
);

CREATE TABLE macs (
    mac        TEXT    PRIMARY KEY,
    device_id  INTEGER NOT NULL REFERENCES devices(id),
    vendor     TEXT    NOT NULL DEFAULT '',
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL
);
CREATE INDEX macs_device ON macs (device_id);

CREATE TABLE mac_locations (
    mac        TEXT    NOT NULL REFERENCES macs(mac) ON DELETE CASCADE,
    port_id    INTEGER NOT NULL REFERENCES ports(id) ON DELETE CASCADE,
    vlan       INTEGER NOT NULL DEFAULT 0,
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    PRIMARY KEY (mac, port_id)
);
CREATE INDEX mac_locations_port ON mac_locations (port_id, last_seen);

CREATE TABLE mac_ip_history (
    mac        TEXT    NOT NULL REFERENCES macs(mac) ON DELETE CASCADE,
    ip         TEXT    NOT NULL,
    source     TEXT    NOT NULL,
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    PRIMARY KEY (mac, ip, source)
);
CREATE INDEX mac_ip_history_ip ON mac_ip_history (ip);

CREATE TABLE mac_hostname_history (
    mac        TEXT    NOT NULL REFERENCES macs(mac) ON DELETE CASCADE,
    hostname   TEXT    NOT NULL,
    source     TEXT    NOT NULL,
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    PRIMARY KEY (mac, hostname, source)
);
CREATE INDEX mac_hostname_history_name ON mac_hostname_history (hostname COLLATE NOCASE);

CREATE TABLE port_groups (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    port_id  INTEGER NOT NULL REFERENCES ports(id) ON DELETE CASCADE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    UNIQUE (event_id, port_id)
);

-- All MACs seen in a port group.
CREATE TABLE port_group_macs (
    group_id   INTEGER NOT NULL REFERENCES port_groups(id) ON DELETE CASCADE,
    mac        TEXT    NOT NULL,
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    PRIMARY KEY (group_id, mac)
);

-- Running total per event, subject and port. The port is part of the key
-- so that "hide in rankings" can be filtered per port at query time.
CREATE TABLE traffic_totals (
    event_id     INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    subject_type TEXT    NOT NULL,  -- mac, port_group, unassigned
    subject_id   TEXT    NOT NULL,  -- MAC, port_groups.id or ports.id
    port_id      INTEGER NOT NULL REFERENCES ports(id) ON DELETE CASCADE,
    bytes_down   INTEGER NOT NULL DEFAULT 0,
    bytes_up     INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (event_id, subject_type, subject_id, port_id)
);
CREATE INDEX traffic_totals_down ON traffic_totals (event_id, bytes_down);
CREATE INDEX traffic_totals_up ON traffic_totals (event_id, bytes_up);
CREATE INDEX traffic_totals_subject ON traffic_totals (subject_type, subject_id);

CREATE TABLE traffic_buckets (
    event_id     INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    subject_type TEXT    NOT NULL,
    subject_id   TEXT    NOT NULL,
    port_id      INTEGER NOT NULL,
    bucket_start INTEGER NOT NULL,  -- 5-minute grid
    bytes_down   INTEGER NOT NULL DEFAULT 0,
    bytes_up     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (event_id, subject_type, subject_id, port_id, bucket_start)
);
CREATE INDEX traffic_buckets_event ON traffic_buckets (event_id, bucket_start);

CREATE TABLE merge_suggestions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    mac_a      TEXT    NOT NULL,
    mac_b      TEXT    NOT NULL,
    reason     TEXT    NOT NULL,
    status     TEXT    NOT NULL DEFAULT 'open',  -- open, accepted, rejected
    event_id   INTEGER,
    created_at INTEGER NOT NULL,
    decided_by INTEGER,
    decided_at INTEGER,
    UNIQUE (mac_a, mac_b)
);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    role          TEXT    NOT NULL,  -- viewer, operator, admin
    language      TEXT    NOT NULL DEFAULT 'en',
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    last_login    INTEGER
);

CREATE TABLE sessions (
    id         TEXT    PRIMARY KEY,  -- SHA-256 of the cookie value
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE api_tokens (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    token_hash  TEXT    NOT NULL UNIQUE,  -- SHA-256; the plaintext is shown only once
    prefix      TEXT    NOT NULL,         -- first characters, for recognition
    scopes      TEXT    NOT NULL,         -- space-separated
    allowed_ips TEXT    NOT NULL DEFAULT '',
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER,
    last_used   INTEGER,
    revoked     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE audit_log (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,
    actor   TEXT    NOT NULL,
    action  TEXT    NOT NULL,
    target  TEXT    NOT NULL DEFAULT '',
    details TEXT    NOT NULL DEFAULT '',
    ip      TEXT    NOT NULL DEFAULT '',
    at      INTEGER NOT NULL
);
CREATE INDEX audit_log_at ON audit_log (at);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE api_tokens;
DROP TABLE sessions;
DROP TABLE users;
DROP TABLE merge_suggestions;
DROP TABLE traffic_buckets;
DROP TABLE traffic_totals;
DROP TABLE port_group_macs;
DROP TABLE port_groups;
DROP TABLE mac_hostname_history;
DROP TABLE mac_ip_history;
DROP TABLE mac_locations;
DROP TABLE macs;
DROP TABLE devices;
DROP TABLE port_traffic_totals;
DROP TABLE events;
DROP TABLE port_counter_state;
DROP TABLE ports;
DROP TABLE switches;
