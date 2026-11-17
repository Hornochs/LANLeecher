-- +goose Up
-- Consecutive forwarding-table reads in which a port looked like an uplink
-- (bridge LLDP neighbour or many MACs). The hint is shown only after a few of
-- them, so a short burst or a single LLDP frame does not raise it.
ALTER TABLE ports ADD COLUMN uplink_streak INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE ports DROP COLUMN uplink_streak;
