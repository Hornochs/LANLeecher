-- +goose Up
-- Topology data collected by polling (LLDP, link aggregation) ...
ALTER TABLE switches ADD COLUMN chassis_id TEXT NOT NULL DEFAULT '';
ALTER TABLE switches ADD COLUMN base_mac TEXT NOT NULL DEFAULT '';
ALTER TABLE ports ADD COLUMN lag_if_index INTEGER NOT NULL DEFAULT 0;  -- ifIndex of the LAG this port is a member of, 0 = none

CREATE TABLE lldp_neighbors (
    switch_id  INTEGER NOT NULL REFERENCES switches(id) ON DELETE CASCADE,
    if_index   INTEGER NOT NULL,
    chassis_id TEXT    NOT NULL DEFAULT '',
    sys_name   TEXT    NOT NULL DEFAULT '',
    port_id    TEXT    NOT NULL DEFAULT '',
    port_desc  TEXT    NOT NULL DEFAULT '',
    bridge     INTEGER NOT NULL DEFAULT 0,
    seen_at    INTEGER NOT NULL,             -- Unix seconds (UTC)
    PRIMARY KEY (switch_id, if_index)
) WITHOUT ROWID;

-- ... and network maps: switches (and internet connections) placed on a background image.
CREATE TABLE maps (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    sort       INTEGER NOT NULL DEFAULT 0,
    refresh    INTEGER NOT NULL DEFAULT 15,  -- seconds between updates in the browser
    bg         BLOB,                         -- background image (PNG, JPEG or GIF), NULL = none
    bg_type    TEXT    NOT NULL DEFAULT '',  -- its media type
    bg_width   INTEGER NOT NULL DEFAULT 0,
    bg_height  INTEGER NOT NULL DEFAULT 0,
    bg_hash    TEXT    NOT NULL DEFAULT '',  -- hex SHA-256 prefix, ETag and cache key
    created_at INTEGER NOT NULL,             -- Unix seconds (UTC)
    updated_at INTEGER NOT NULL
);

-- Switches on a map; x and y are fractions (0-1) of the map's width and height.
CREATE TABLE map_nodes (
    map_id    INTEGER NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    switch_id INTEGER NOT NULL REFERENCES switches(id) ON DELETE CASCADE,
    x         REAL    NOT NULL,
    y         REAL    NOT NULL,
    PRIMARY KEY (map_id, switch_id)
) WITHOUT ROWID;

-- Internet connections (WAN links) on a map.
CREATE TABLE map_wan_nodes (
    map_id      INTEGER NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    wan_link_id INTEGER NOT NULL REFERENCES wan_links(id) ON DELETE CASCADE,
    x           REAL    NOT NULL,
    y           REAL    NOT NULL,
    PRIMARY KEY (map_id, wan_link_id)
) WITHOUT ROWID;

-- Links drawn by hand ("manual") and automatic links hidden by an admin ("hidden").
CREATE TABLE map_links (
    id       INTEGER PRIMARY KEY,
    map_id   INTEGER NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    kind     TEXT    NOT NULL,               -- manual, hidden
    a_switch INTEGER NOT NULL REFERENCES switches(id) ON DELETE CASCADE,
    a_if     INTEGER NOT NULL DEFAULT 0,     -- ifIndex, 0 = unknown
    b_switch INTEGER NOT NULL REFERENCES switches(id) ON DELETE CASCADE,
    b_if     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX map_links_map ON map_links(map_id);

-- +goose Down
DROP TABLE map_links;
DROP TABLE map_wan_nodes;
DROP TABLE map_nodes;
DROP TABLE maps;
DROP TABLE lldp_neighbors;
ALTER TABLE ports DROP COLUMN lag_if_index;
ALTER TABLE switches DROP COLUMN base_mac;
ALTER TABLE switches DROP COLUMN chassis_id;
