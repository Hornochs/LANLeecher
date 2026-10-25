package store

import (
	"context"
	"time"
)

// BucketSize is the resolution of traffic_buckets.
const BucketSize = 5 * time.Minute

// Subject types in traffic_totals.
const (
	SubjectMAC        = "mac"
	SubjectPortGroup  = "port_group"
	SubjectUnassigned = "unassigned"
)

// AddPortTraffic books a delta on the port itself (every role, including uplinks).
func (tx *Tx) AddPortTraffic(ctx context.Context, eventID, portID int64, down, up uint64, at time.Time) error {
	_, err := tx.tx.ExecContext(ctx, `INSERT INTO port_traffic_totals (event_id, port_id, bytes_down, bytes_up, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (event_id, port_id) DO UPDATE SET bytes_down = bytes_down + excluded.bytes_down,
			bytes_up = bytes_up + excluded.bytes_up, updated_at = excluded.updated_at`,
		eventID, portID, int64(down), int64(up), unix(at))
	return err
}

// AddSubjectTraffic books a delta on a ranking subject and its 5-minute bucket.
func (tx *Tx) AddSubjectTraffic(ctx context.Context, eventID int64, subjectType, subjectID string, portID int64,
	down, up uint64, at time.Time) error {
	if _, err := tx.tx.ExecContext(ctx, `INSERT INTO traffic_totals
		(event_id, subject_type, subject_id, port_id, bytes_down, bytes_up, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (event_id, subject_type, subject_id, port_id) DO UPDATE SET
			bytes_down = bytes_down + excluded.bytes_down, bytes_up = bytes_up + excluded.bytes_up,
			updated_at = excluded.updated_at`,
		eventID, subjectType, subjectID, portID, int64(down), int64(up), unix(at)); err != nil {
		return err
	}
	bucket := at.UTC().Truncate(BucketSize)
	_, err := tx.tx.ExecContext(ctx, `INSERT INTO traffic_buckets
		(event_id, subject_type, subject_id, port_id, bucket_start, bytes_down, bytes_up) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (event_id, subject_type, subject_id, port_id, bucket_start) DO UPDATE SET
			bytes_down = bytes_down + excluded.bytes_down, bytes_up = bytes_up + excluded.bytes_up`,
		eventID, subjectType, subjectID, portID, unix(bucket), int64(down), int64(up))
	return err
}

// EnsurePortGroup returns the port group of a port in an event, creating it.
func (tx *Tx) EnsurePortGroup(ctx context.Context, eventID, portID int64) (int64, error) {
	if _, err := tx.tx.ExecContext(ctx, `INSERT INTO port_groups (event_id, port_id) VALUES (?, ?)
		ON CONFLICT (event_id, port_id) DO NOTHING`, eventID, portID); err != nil {
		return 0, err
	}
	var id int64
	err := tx.tx.QueryRowContext(ctx, `SELECT id FROM port_groups WHERE event_id = ? AND port_id = ?`, eventID, portID).Scan(&id)
	return id, err
}

// TouchPortGroupMAC records that a MAC was part of a port group.
func (tx *Tx) TouchPortGroupMAC(ctx context.Context, groupID int64, mac string, at time.Time) error {
	_, err := tx.tx.ExecContext(ctx, `INSERT INTO port_group_macs (group_id, mac, first_seen, last_seen) VALUES (?, ?, ?, ?)
		ON CONFLICT (group_id, mac) DO UPDATE SET last_seen = excluded.last_seen`, groupID, mac, unix(at), unix(at))
	return err
}

// PortTraffic is the traffic of a port in an event.
type PortTraffic struct {
	BytesDown int64
	BytesUp   int64
}

// PortTrafficBySwitch returns per-port totals of an event for a switch.
func (db *DB) PortTrafficBySwitch(ctx context.Context, eventID, switchID int64) (map[int64]PortTraffic, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT t.port_id, t.bytes_down, t.bytes_up FROM port_traffic_totals t
		JOIN ports p ON p.id = t.port_id WHERE t.event_id = ? AND p.switch_id = ?`, eventID, switchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]PortTraffic{}
	for rows.Next() {
		var id int64
		var t PortTraffic
		if err := rows.Scan(&id, &t.BytesDown, &t.BytesUp); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// PortTrafficRow is one port in an event-wide listing.
type PortTrafficRow struct {
	Port      *Port
	BytesDown int64
	BytesUp   int64
	// Unassigned is the part that could not be attributed to any MAC.
	UnassignedDown int64
	UnassignedUp   int64
}

// PortTrafficByEvent lists the traffic of all ports in an event, hidden ones included.
func (db *DB) PortTrafficByEvent(ctx context.Context, eventID int64) ([]PortTrafficRow, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+portCols+`, t.bytes_down, t.bytes_up,
			COALESCE((SELECT SUM(u.bytes_down) FROM traffic_totals u WHERE u.event_id = t.event_id
				AND u.subject_type = 'unassigned' AND u.port_id = t.port_id), 0),
			COALESCE((SELECT SUM(u.bytes_up) FROM traffic_totals u WHERE u.event_id = t.event_id
				AND u.subject_type = 'unassigned' AND u.port_id = t.port_id), 0)
		FROM port_traffic_totals t JOIN ports p ON p.id = t.port_id JOIN switches s ON s.id = p.switch_id
		WHERE t.event_id = ? ORDER BY t.bytes_down + t.bytes_up DESC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PortTrafficRow
	for rows.Next() {
		var p Port
		var seen int64
		var role string
		var r PortTrafficRow
		dest := append(portDest(&p, &role, &seen), &r.BytesDown, &r.BytesUp, &r.UnassignedDown, &r.UnassignedUp)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		p.Role = roleOf(role)
		p.LastSeen = fromUnix(seen)
		r.Port = &p
		out = append(out, r)
	}
	return out, rows.Err()
}

// Bucket is one point of a time series.
type Bucket struct {
	Start     time.Time
	BytesDown int64
	BytesUp   int64
}

// EventTimeseries sums all non-uplink traffic of an event per 5 minutes.
func (db *DB) EventTimeseries(ctx context.Context, eventID int64) ([]Bucket, error) {
	return db.buckets(ctx, `SELECT b.bucket_start, SUM(b.bytes_down), SUM(b.bytes_up) FROM traffic_buckets b
		WHERE b.event_id = ? GROUP BY b.bucket_start ORDER BY b.bucket_start`, eventID)
}

// DeviceTimeseries returns the 5-minute buckets of a device in an event.
func (db *DB) DeviceTimeseries(ctx context.Context, deviceID, eventID int64) ([]Bucket, error) {
	return db.buckets(ctx, `SELECT b.bucket_start, SUM(b.bytes_down), SUM(b.bytes_up) FROM traffic_buckets b
		JOIN macs m ON m.mac = b.subject_id
		WHERE b.subject_type = 'mac' AND m.device_id = ? AND b.event_id = ?
		GROUP BY b.bucket_start ORDER BY b.bucket_start`, deviceID, eventID)
}

func (db *DB) buckets(ctx context.Context, query string, args ...any) ([]Bucket, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bucket
	for rows.Next() {
		var b Bucket
		var start int64
		if err := rows.Scan(&start, &b.BytesDown, &b.BytesUp); err != nil {
			return nil, err
		}
		b.Start = fromUnix(start)
		out = append(out, b)
	}
	return out, rows.Err()
}

// WindowTotals sums the 5-minute buckets of an event in [from, to): all
// attributed traffic (the event timeline) and the part without a MAC.
func (db *DB) WindowTotals(ctx context.Context, eventID int64, from, to time.Time) (all, unassigned PortTraffic, err error) {
	err = db.sql.QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes_down), 0), COALESCE(SUM(bytes_up), 0),
			COALESCE(SUM(CASE WHEN subject_type = 'unassigned' THEN bytes_down END), 0),
			COALESCE(SUM(CASE WHEN subject_type = 'unassigned' THEN bytes_up END), 0)
		FROM traffic_buckets WHERE event_id = ? AND bucket_start >= ? AND bucket_start < ?`,
		eventID, unix(from.Truncate(BucketSize)), unix(to)).
		Scan(&all.BytesDown, &all.BytesUp, &unassigned.BytesDown, &unassigned.BytesUp)
	return all, unassigned, err
}
