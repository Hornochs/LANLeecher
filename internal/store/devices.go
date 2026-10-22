package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Device groups one or more MACs (after a merge) that belong to one machine.
type Device struct {
	ID          int64
	DisplayName string
	Hidden      bool
	Notes       string
	CreatedAt   time.Time
	MACs        []DeviceMAC
}

// DeviceMAC is a MAC of a device.
type DeviceMAC struct {
	MAC       string
	Vendor    string
	FirstSeen time.Time
	LastSeen  time.Time
}

// GetDevice returns a device with its MACs.
func (db *DB) GetDevice(ctx context.Context, id int64) (*Device, error) {
	var d Device
	var created int64
	err := db.sql.QueryRowContext(ctx, `SELECT id, display_name, hidden_in_rankings, notes, created_at FROM devices WHERE id = ?`, id).
		Scan(&d.ID, &d.DisplayName, &d.Hidden, &d.Notes, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.CreatedAt = fromUnix(created)
	rows, err := db.sql.QueryContext(ctx, `SELECT mac, vendor, first_seen, last_seen FROM macs WHERE device_id = ? ORDER BY last_seen DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m DeviceMAC
		var first, last int64
		if err := rows.Scan(&m.MAC, &m.Vendor, &first, &last); err != nil {
			return nil, err
		}
		m.FirstSeen, m.LastSeen = fromUnix(first), fromUnix(last)
		d.MACs = append(d.MACs, m)
	}
	return &d, rows.Err()
}

// UpdateDevice saves display name, visibility and notes.
func (db *DB) UpdateDevice(ctx context.Context, id int64, name string, hidden bool, notes string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE devices SET display_name = ?, hidden_in_rankings = ?, notes = ? WHERE id = ?`,
		name, boolInt(hidden), notes, id)
	return err
}

// DeviceEventTotal is the traffic of a device in one event.
type DeviceEventTotal struct {
	Event     *Event
	BytesDown int64
	BytesUp   int64
}

// DeviceEvents returns the per-event totals of a device, newest event first.
// limit 0 returns all events.
func (db *DB) DeviceEvents(ctx context.Context, deviceID int64, limit int) ([]DeviceEventTotal, error) {
	query := `SELECT e.id, e.name, e.slug, e.started_at, e.ended_at, e.notes, SUM(t.bytes_down), SUM(t.bytes_up)
		FROM traffic_totals t JOIN macs m ON m.mac = t.subject_id JOIN events e ON e.id = t.event_id
		WHERE t.subject_type = 'mac' AND m.device_id = ? GROUP BY e.id ORDER BY e.started_at DESC`
	args := []any{deviceID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceEventTotal
	for rows.Next() {
		var e Event
		var started int64
		var ended sql.NullInt64
		var t DeviceEventTotal
		if err := rows.Scan(&e.ID, &e.Name, &e.Slug, &started, &ended, &e.Notes, &t.BytesDown, &t.BytesUp); err != nil {
			return nil, err
		}
		e.StartedAt, e.EndedAt = fromUnix(started), fromNullUnix(ended)
		t.Event = &e
		out = append(out, t)
	}
	return out, rows.Err()
}

// HistoryRow is an entry of the IP or hostname history.
type HistoryRow struct {
	MAC       string
	Value     string
	Source    string
	FirstSeen time.Time
	LastSeen  time.Time
}

// DeviceIPHistory returns all IPs of a device's MACs.
func (db *DB) DeviceIPHistory(ctx context.Context, deviceID int64) ([]HistoryRow, error) {
	return db.history(ctx, `SELECT h.mac, h.ip, h.source, h.first_seen, h.last_seen FROM mac_ip_history h
		JOIN macs m ON m.mac = h.mac WHERE m.device_id = ? ORDER BY h.last_seen DESC`, deviceID)
}

// DeviceHostnameHistory returns all hostnames of a device's MACs.
func (db *DB) DeviceHostnameHistory(ctx context.Context, deviceID int64) ([]HistoryRow, error) {
	return db.history(ctx, `SELECT h.mac, h.hostname, h.source, h.first_seen, h.last_seen FROM mac_hostname_history h
		JOIN macs m ON m.mac = h.mac WHERE m.device_id = ? ORDER BY h.last_seen DESC`, deviceID)
}

// MACsByIP returns the history rows of an IP.
func (db *DB) MACsByIP(ctx context.Context, ip string) ([]HistoryRow, error) {
	return db.history(ctx, `SELECT mac, ip, source, first_seen, last_seen FROM mac_ip_history WHERE ip = ?
		ORDER BY last_seen DESC`, ip)
}

// MACsByHostname returns the history rows of a hostname (case-insensitive).
func (db *DB) MACsByHostname(ctx context.Context, name string) ([]HistoryRow, error) {
	return db.history(ctx, `SELECT mac, hostname, source, first_seen, last_seen FROM mac_hostname_history
		WHERE hostname = ? COLLATE NOCASE ORDER BY last_seen DESC`, name)
}

func (db *DB) history(ctx context.Context, query string, args ...any) ([]HistoryRow, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryRow
	for rows.Next() {
		var h HistoryRow
		var first, last int64
		if err := rows.Scan(&h.MAC, &h.Value, &h.Source, &first, &last); err != nil {
			return nil, err
		}
		h.FirstSeen, h.LastSeen = fromUnix(first), fromUnix(last)
		out = append(out, h)
	}
	return out, rows.Err()
}

// LocationRow is a port where a MAC was learned.
type LocationRow struct {
	MAC       string
	PortID    int64
	PortName  string
	VLAN      int
	FirstSeen time.Time
	LastSeen  time.Time
}

// DeviceLocations returns the ports where a device's MACs were seen.
func (db *DB) DeviceLocations(ctx context.Context, deviceID int64) ([]LocationRow, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT l.mac, p.id, s.name, p.if_name, p.if_descr, p.if_index, p.label,
			l.vlan, l.first_seen, l.last_seen
		FROM mac_locations l JOIN macs m ON m.mac = l.mac JOIN ports p ON p.id = l.port_id
		JOIN switches s ON s.id = p.switch_id WHERE m.device_id = ? ORDER BY l.last_seen DESC`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocationRow
	for rows.Next() {
		var l LocationRow
		var p Port
		var first, last int64
		if err := rows.Scan(&l.MAC, &l.PortID, &p.SwitchName, &p.IfName, &p.IfDescr, &p.IfIndex, &p.Label, &l.VLAN,
			&first, &last); err != nil {
			return nil, err
		}
		l.PortName = p.Name()
		l.FirstSeen, l.LastSeen = fromUnix(first), fromUnix(last)
		out = append(out, l)
	}
	return out, rows.Err()
}

// MergeSuggestion proposes that two MACs belong to the same device.
type MergeSuggestion struct {
	ID        int64
	MACA      string
	MACB      string
	DeviceA   int64
	DeviceB   int64
	Reason    string
	Status    string
	CreatedAt time.Time
}

// CreateMergeSuggestion stores a suggestion unless one exists for the pair.
func (db *DB) CreateMergeSuggestion(ctx context.Context, macA, macB, reason string, eventID int64) (bool, error) {
	if macA > macB {
		macA, macB = macB, macA
	}
	res, err := db.sql.ExecContext(ctx, `INSERT INTO merge_suggestions (mac_a, mac_b, reason, event_id, created_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (mac_a, mac_b) DO NOTHING`, macA, macB, reason, eventID, unix(Now()))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListMergeSuggestions returns open suggestions, optionally for one device.
func (db *DB) ListMergeSuggestions(ctx context.Context, deviceID int64) ([]MergeSuggestion, error) {
	query := `SELECT s.id, s.mac_a, s.mac_b, a.device_id, b.device_id, s.reason, s.status, s.created_at
		FROM merge_suggestions s JOIN macs a ON a.mac = s.mac_a JOIN macs b ON b.mac = s.mac_b
		WHERE s.status = 'open' AND a.device_id <> b.device_id`
	var args []any
	if deviceID != 0 {
		query += ` AND (a.device_id = ? OR b.device_id = ?)`
		args = append(args, deviceID, deviceID)
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY s.created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MergeSuggestion
	for rows.Next() {
		var m MergeSuggestion
		var created int64
		if err := rows.Scan(&m.ID, &m.MACA, &m.MACB, &m.DeviceA, &m.DeviceB, &m.Reason, &m.Status, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = fromUnix(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

// DecideMergeSuggestion accepts (merging B's device into A's) or rejects a suggestion.
func (db *DB) DecideMergeSuggestion(ctx context.Context, id int64, accept bool, userID int64) error {
	var macA, macB string
	if err := db.sql.QueryRowContext(ctx, `SELECT mac_a, mac_b FROM merge_suggestions WHERE id = ? AND status = 'open'`, id).
		Scan(&macA, &macB); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	status := "rejected"
	if accept {
		status = "accepted"
	}
	return db.InTx(ctx, func(tx *Tx) error {
		if accept {
			var keep, drop int64
			if err := tx.tx.QueryRowContext(ctx, `SELECT device_id FROM macs WHERE mac = ?`, macA).Scan(&keep); err != nil {
				return err
			}
			if err := tx.tx.QueryRowContext(ctx, `SELECT device_id FROM macs WHERE mac = ?`, macB).Scan(&drop); err != nil {
				return err
			}
			if err := tx.mergeDevices(ctx, keep, drop); err != nil {
				return err
			}
		}
		_, err := tx.tx.ExecContext(ctx, `UPDATE merge_suggestions SET status = ?, decided_by = ?, decided_at = ? WHERE id = ?`,
			status, userID, unix(Now()), id)
		return err
	})
}

// MergeDevices moves all MACs of drop into keep and deletes drop.
func (db *DB) MergeDevices(ctx context.Context, keep, drop int64) error {
	return db.InTx(ctx, func(tx *Tx) error { return tx.mergeDevices(ctx, keep, drop) })
}

func (tx *Tx) mergeDevices(ctx context.Context, keep, drop int64) error {
	if keep == drop {
		return nil
	}
	// Keep the stricter visibility and the first non-empty name.
	if _, err := tx.tx.ExecContext(ctx, `UPDATE devices SET
			hidden_in_rankings = MAX(hidden_in_rankings, (SELECT hidden_in_rankings FROM devices WHERE id = ?)),
			display_name = CASE WHEN display_name = '' THEN (SELECT display_name FROM devices WHERE id = ?) ELSE display_name END
		WHERE id = ?`, drop, drop, keep); err != nil {
		return err
	}
	if _, err := tx.tx.ExecContext(ctx, `UPDATE macs SET device_id = ? WHERE device_id = ?`, keep, drop); err != nil {
		return err
	}
	_, err := tx.tx.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, drop)
	return err
}

// SplitMAC moves a MAC into a new device of its own (undo of a merge).
func (db *DB) SplitMAC(ctx context.Context, mac string) (int64, error) {
	var id int64
	err := db.InTx(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `INSERT INTO devices (created_at) VALUES (?)`, unix(Now()))
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE macs SET device_id = ? WHERE mac = ?`, id, mac)
		return err
	})
	return id, err
}
