-- +goose Up
-- An operator confirmed that a port is not an uplink although the heuristic
-- (many MACs or an LLDP neighbor) says so, e.g. a hypervisor with many VMs.
-- The "probably uplink" hint stays off until the confirmation is withdrawn
-- or the port becomes an uplink.
ALTER TABLE ports ADD COLUMN uplink_dismissed INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE ports DROP COLUMN uplink_dismissed;
