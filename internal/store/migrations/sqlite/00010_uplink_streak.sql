-- +goose Up
-- Consecutive forwarding-table reads in which a port looked like an uplink
-- (bridge LLDP neighbour or many MACs). The hint is shown only after a few of
-- them, so a short burst or a single LLDP frame does not raise it.
ALTER TABLE ports ADD COLUMN uplink_streak INTEGER NOT NULL DEFAULT 0;

-- Time of the last forwarding-table read per switch. A MAC location counts as
-- current only if that read still saw it (a device that moved to another
-- port is no "MAC on two ports" once its old switch was read again).
ALTER TABLE switches ADD COLUMN fdb_read_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE switches DROP COLUMN fdb_read_at;
ALTER TABLE ports DROP COLUMN uplink_streak;
