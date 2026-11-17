-- +goose Up
-- Named internet uplinks. Ports with the role "wan" belong to one of them, so
-- that several internet uplinks of a party can be reported separately.
CREATE TABLE wan_links (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    sort       INTEGER NOT NULL DEFAULT 0,
    down_mbps  INTEGER NOT NULL DEFAULT 0,  -- booked bandwidth, 0 = unknown
    up_mbps    INTEGER NOT NULL DEFAULT 0,
    notes      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

ALTER TABLE ports ADD COLUMN wan_link_id INTEGER REFERENCES wan_links(id) ON DELETE SET NULL;

-- Traffic per port in 5-minute buckets (every role), in client view like
-- port_traffic_totals.
CREATE TABLE port_traffic_buckets (
    event_id     INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    port_id      INTEGER NOT NULL REFERENCES ports(id) ON DELETE CASCADE,
    bucket_start INTEGER NOT NULL,
    bytes_down   INTEGER NOT NULL DEFAULT 0,
    bytes_up     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (event_id, port_id, bucket_start)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE port_traffic_buckets;
ALTER TABLE ports DROP COLUMN wan_link_id;
DROP TABLE wan_links;
