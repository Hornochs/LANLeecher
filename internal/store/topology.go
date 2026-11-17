package store

import (
	"context"
	"time"
)

// LLDPNeighbor is a system a switch sees on a port.
type LLDPNeighbor struct {
	IfIndex   int
	ChassisID string
	SysName   string
	PortID    string
	PortDesc  string
	Bridge    bool
	SeenAt    time.Time
}

// RecordTopology stores what a poll learned for the network map. A nil
// neighbors or lag map means "not read, keep the stored data"; an empty map
// replaces the stored data with nothing. Empty chassisID or baseMAC keep the
// stored value. lag maps member ifIndex to LAG ifIndex.
func (tx *Tx) RecordTopology(ctx context.Context, switchID int64, chassisID, baseMAC string,
	neighbors map[int]LLDPNeighbor, lag map[int]int, at time.Time) error {
	if chassisID != "" {
		if _, err := tx.tx.ExecContext(ctx, `UPDATE switches SET chassis_id = ? WHERE id = ?`, chassisID, switchID); err != nil {
			return err
		}
	}
	if baseMAC != "" {
		if _, err := tx.tx.ExecContext(ctx, `UPDATE switches SET base_mac = ? WHERE id = ?`, baseMAC, switchID); err != nil {
			return err
		}
	}
	if neighbors != nil {
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM lldp_neighbors WHERE switch_id = ?`, switchID); err != nil {
			return err
		}
		for ifIndex, n := range neighbors {
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO lldp_neighbors
				(switch_id, if_index, chassis_id, sys_name, port_id, port_desc, bridge, seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				switchID, ifIndex, n.ChassisID, n.SysName, n.PortID, n.PortDesc, boolInt(n.Bridge), unix(at)); err != nil {
				return err
			}
		}
	}
	if lag != nil {
		if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET lag_if_index = 0 WHERE switch_id = ? AND lag_if_index <> 0`, switchID); err != nil {
			return err
		}
		for member, agg := range lag {
			if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET lag_if_index = ? WHERE switch_id = ? AND if_index = ?`,
				agg, switchID, member); err != nil {
				return err
			}
		}
	}
	return nil
}

// ListLLDPNeighbors returns the stored neighbors of all switches by switch ID.
func (db *DB) ListLLDPNeighbors(ctx context.Context) (map[int64][]LLDPNeighbor, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT switch_id, if_index, chassis_id, sys_name, port_id, port_desc, bridge, seen_at
		FROM lldp_neighbors ORDER BY switch_id, if_index`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]LLDPNeighbor{}
	for rows.Next() {
		var id, seen int64
		var n LLDPNeighbor
		if err := rows.Scan(&id, &n.IfIndex, &n.ChassisID, &n.SysName, &n.PortID, &n.PortDesc, &n.Bridge, &seen); err != nil {
			return nil, err
		}
		n.SeenAt = fromUnix(seen)
		out[id] = append(out[id], n)
	}
	return out, rows.Err()
}

// ListAllPorts returns the ports of all switches ordered by switch and ifIndex.
func (db *DB) ListAllPorts(ctx context.Context) ([]*Port, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+portCols+` FROM ports p JOIN switches s ON s.id = p.switch_id
		ORDER BY p.switch_id, p.if_index`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Port
	for rows.Next() {
		p, err := scanPort(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
