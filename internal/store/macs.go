package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
)

func roleOf(s string) accounting.Role { return accounting.Role(s) }

// Sighting is a MAC learned on a port.
type Sighting struct {
	MAC    string
	PortID int64
	VLAN   int
	// Uplink marks sightings on uplink ports. They count for the uplink
	// heuristic but are not stored as locations: behind an uplink every MAC
	// of the network shows up.
	Uplink bool
}

// RecordSightings stores MAC locations from a forwarding-table read. Unknown
// MACs get their own device. It also refreshes mac_count, the LLDP neighbor
// and the uplink heuristic of the switch's ports.
func (tx *Tx) RecordSightings(ctx context.Context, switchID int64, sightings []Sighting, lldp map[int64]string,
	uplinkThreshold int, at time.Time) error {
	now := unix(at)
	counts := map[int64]map[string]bool{}
	for _, s := range sightings {
		if counts[s.PortID] == nil {
			counts[s.PortID] = map[string]bool{}
		}
		counts[s.PortID][s.MAC] = true
		if s.Uplink {
			continue
		}
		if err := tx.ensureMAC(ctx, s.MAC, at); err != nil {
			return err
		}
		if _, err := tx.tx.ExecContext(ctx, `INSERT INTO mac_locations (mac, port_id, vlan, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT (mac, port_id) DO UPDATE SET last_seen = excluded.last_seen, vlan = excluded.vlan`,
			s.MAC, s.PortID, s.VLAN, now, now); err != nil {
			return err
		}
	}
	if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET mac_count = 0, uplink_suspect = 0, lldp_neighbor = ''
		WHERE switch_id = ?`, switchID); err != nil {
		return err
	}
	for portID, macs := range counts {
		if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET mac_count = ?, uplink_suspect = ? WHERE id = ?`,
			len(macs), boolInt(len(macs) > uplinkThreshold), portID); err != nil {
			return err
		}
	}
	for portID, neighbor := range lldp {
		if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET lldp_neighbor = ?, uplink_suspect = 1 WHERE id = ?`,
			neighbor, portID); err != nil {
			return err
		}
	}
	// Ports already confirmed as uplink need no hint.
	_, err := tx.tx.ExecContext(ctx, `UPDATE ports SET uplink_suspect = 0 WHERE switch_id = ? AND role = 'uplink'`, switchID)
	return err
}

func (tx *Tx) ensureMAC(ctx context.Context, mac string, at time.Time) error {
	res, err := tx.tx.ExecContext(ctx, `UPDATE macs SET last_seen = ? WHERE mac = ?`, unix(at), mac)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	dev, err := tx.tx.ExecContext(ctx, `INSERT INTO devices (created_at) VALUES (?)`, unix(at))
	if err != nil {
		return err
	}
	devID, err := dev.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.tx.ExecContext(ctx, `INSERT INTO macs (mac, device_id, first_seen, last_seen) VALUES (?, ?, ?, ?)`,
		mac, devID, unix(at), unix(at))
	return err
}

// EnsureMAC makes sure a MAC exists (used by resolvers for MACs seen only via ARP).
func (db *DB) EnsureMAC(ctx context.Context, mac string, at time.Time) error {
	return db.InTx(ctx, func(tx *Tx) error { return tx.ensureMAC(ctx, mac, at) })
}

// MACKnown reports whether a MAC is in the database.
func (db *DB) MACKnown(ctx context.Context, mac string) (bool, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM macs WHERE mac = ?`, mac).Scan(&n)
	return n > 0, err
}

// RecordIP stores a MAC/IP pair. It returns true if the pair is new.
func (db *DB) RecordIP(ctx context.Context, mac, ip, source string, at time.Time) (bool, error) {
	res, err := db.sql.ExecContext(ctx, `UPDATE mac_ip_history SET last_seen = ? WHERE mac = ? AND ip = ? AND source = ?`,
		unix(at), mac, ip, source)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return false, nil
	}
	_, err = db.sql.ExecContext(ctx, `INSERT INTO mac_ip_history (mac, ip, source, first_seen, last_seen) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (mac, ip, source) DO UPDATE SET last_seen = excluded.last_seen`, mac, ip, source, unix(at), unix(at))
	return err == nil, err
}

// RecordHostname stores a MAC/hostname pair. It returns true if the pair is new.
func (db *DB) RecordHostname(ctx context.Context, mac, hostname, source string, at time.Time) (bool, error) {
	res, err := db.sql.ExecContext(ctx, `UPDATE mac_hostname_history SET last_seen = ? WHERE mac = ? AND hostname = ? AND source = ?`,
		unix(at), mac, hostname, source)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return false, nil
	}
	_, err = db.sql.ExecContext(ctx, `INSERT INTO mac_hostname_history (mac, hostname, source, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (mac, hostname, source) DO UPDATE SET last_seen = excluded.last_seen`,
		mac, hostname, source, unix(at), unix(at))
	return err == nil, err
}

// ActiveIP is a MAC/IP pair seen recently, for periodic name probes.
type ActiveIP struct {
	MAC string
	IP  string
}

// ActiveIPs returns the latest IP per MAC seen since the given time.
func (db *DB) ActiveIPs(ctx context.Context, since time.Time) ([]ActiveIP, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT mac, ip FROM mac_ip_history WHERE last_seen >= ?
		GROUP BY mac, ip ORDER BY mac`, unix(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActiveIP
	for rows.Next() {
		var a ActiveIP
		if err := rows.Scan(&a.MAC, &a.IP); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CurrentMACs returns the MACs per port of a switch seen since the cutoff.
func (db *DB) CurrentMACs(ctx context.Context, switchID int64, since time.Time) (map[int64][]string, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT l.port_id, l.mac FROM mac_locations l JOIN ports p ON p.id = l.port_id
		WHERE p.switch_id = ? AND l.last_seen >= ? ORDER BY l.mac`, switchID, unix(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var mac string
		if err := rows.Scan(&id, &mac); err != nil {
			return nil, err
		}
		out[id] = append(out[id], mac)
	}
	return out, rows.Err()
}

// MACInfo is a MAC with its best-known names, for display.
type MACInfo struct {
	MAC      string
	DeviceID int64
	IP       string
	Hostname string
	LastSeen time.Time
}

// MACInfos returns display info for the given MACs.
func (db *DB) MACInfos(ctx context.Context, macs []string) (map[string]MACInfo, error) {
	out := map[string]MACInfo{}
	if len(macs) == 0 {
		return out, nil
	}
	ph, args := inList(macs)
	rows, err := db.sql.QueryContext(ctx, `SELECT mac, device_id, last_seen FROM macs WHERE mac IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var m MACInfo
		var seen int64
		if err := rows.Scan(&m.MAC, &m.DeviceID, &seen); err != nil {
			rows.Close()
			return nil, err
		}
		m.LastSeen = fromUnix(seen)
		out[m.MAC] = m
	}
	rows.Close()
	ips, err := db.latestIPs(ctx, macs)
	if err != nil {
		return nil, err
	}
	names, err := db.bestHostnames(ctx, macs)
	if err != nil {
		return nil, err
	}
	for mac, m := range out {
		m.IP = ips[mac]
		m.Hostname = names[mac]
		out[mac] = m
	}
	return out, nil
}

func (db *DB) latestIPs(ctx context.Context, macs []string) (map[string]string, error) {
	ph, args := inList(macs)
	rows, err := db.sql.QueryContext(ctx, `SELECT mac, ip FROM mac_ip_history WHERE mac IN (`+ph+`)
		ORDER BY last_seen ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var mac, ip string
		if err := rows.Scan(&mac, &ip); err != nil {
			return nil, err
		}
		// Later rows are newer and win. IPv4 is preferred for display.
		if prev, ok := out[mac]; ok && !strings.Contains(prev, ":") && strings.Contains(ip, ":") {
			continue
		}
		out[mac] = ip
	}
	return out, rows.Err()
}

// bestHostnames picks one hostname per MAC by source priority, then recency.
func (db *DB) bestHostnames(ctx context.Context, macs []string) (map[string]string, error) {
	ph, args := inList(macs)
	rows, err := db.sql.QueryContext(ctx, `SELECT mac, hostname, source, last_seen FROM mac_hostname_history
		WHERE mac IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type cand struct {
		name string
		prio int
		seen int64
	}
	best := map[string]cand{}
	for rows.Next() {
		var mac, name, source string
		var seen int64
		if err := rows.Scan(&mac, &name, &source, &seen); err != nil {
			return nil, err
		}
		c := cand{name: name, prio: db.sourcePriority(source), seen: seen}
		if b, ok := best[mac]; !ok || c.prio < b.prio || (c.prio == b.prio && c.seen > b.seen) {
			best[mac] = c
		}
	}
	out := make(map[string]string, len(best))
	for mac, c := range best {
		out[mac] = c.name
	}
	return out, rows.Err()
}

func (db *DB) sourcePriority(source string) int {
	for i, s := range db.HostnamePriority {
		if s == source {
			return i
		}
	}
	return len(db.HostnamePriority)
}

// MACsWithHostname returns MACs (other than exclude) that had hostname since a time.
func (db *DB) MACsWithHostname(ctx context.Context, hostname, exclude string, since time.Time) ([]string, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT DISTINCT mac FROM mac_hostname_history
		WHERE hostname = ? COLLATE NOCASE AND mac <> ? AND last_seen >= ?`, hostname, exclude, unix(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var mac string
		if err := rows.Scan(&mac); err != nil {
			return nil, err
		}
		out = append(out, mac)
	}
	return out, rows.Err()
}

// DeviceOfMAC returns the device ID of a MAC.
func (db *DB) DeviceOfMAC(ctx context.Context, mac string) (int64, error) {
	var id int64
	err := db.sql.QueryRowContext(ctx, `SELECT device_id FROM macs WHERE mac = ?`, mac).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

func inList(vals []string) (string, []any) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",")
	args := make([]any, len(vals))
	for i, v := range vals {
		args[i] = v
	}
	return ph, args
}

func inListInt(vals []int64) (string, []any) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",")
	args := make([]any, len(vals))
	for i, v := range vals {
		args[i] = v
	}
	return ph, args
}
