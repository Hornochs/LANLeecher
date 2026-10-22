-- +goose Up
-- Kea DHCP servers that LANLeecher reads leases from (MAC -> IP and hostname).
CREATE TABLE dhcp_sources (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    name                 TEXT    NOT NULL UNIQUE,
    url                  TEXT    NOT NULL,
    kind                 TEXT    NOT NULL DEFAULT 'agent',  -- agent (Kea < 3), dhcp4, dhcp6 (Kea >= 3)
    dhcp4                INTEGER NOT NULL DEFAULT 1,        -- agent: query the DHCPv4 server
    dhcp6                INTEGER NOT NULL DEFAULT 0,        -- agent: query the DHCPv6 server
    username             TEXT    NOT NULL DEFAULT '',
    secrets_enc          BLOB    NOT NULL,                  -- AES-GCM: password, client key
    ca_pem               TEXT    NOT NULL DEFAULT '',       -- extra trusted CA or server certificate
    client_cert_pem      TEXT    NOT NULL DEFAULT '',       -- mTLS
    pinned_sha256        TEXT    NOT NULL DEFAULT '',       -- trusted server certificate (accepted by an admin)
    pinned_cert_pem      TEXT    NOT NULL DEFAULT '',
    insecure_skip_verify INTEGER NOT NULL DEFAULT 0,
    poll_interval        INTEGER NOT NULL DEFAULT 30,       -- seconds
    enabled              INTEGER NOT NULL DEFAULT 1,
    last_poll_at         INTEGER,
    last_poll_ok         INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT    NOT NULL DEFAULT '',
    cert_problem         TEXT    NOT NULL DEFAULT '',       -- '', untrusted or changed
    presented_cert_pem   TEXT    NOT NULL DEFAULT '',       -- certificate seen at the last connection
    kea_version          TEXT    NOT NULL DEFAULT '',
    lease_count          INTEGER NOT NULL DEFAULT 0,
    created_at           INTEGER NOT NULL
);

-- +goose Down
DROP TABLE dhcp_sources;
