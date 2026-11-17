package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
	"github.com/lan-dot-party/lanleecher/internal/oui"
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

// UplinkConfirmReads is how many forwarding-table reads in a row a port must
// look like an uplink before it gets the "probably uplink" hint.
const UplinkConfirmReads = 3

// PortNeighbor is what LLDP reports on a port.
type PortNeighbor struct {
	Name string
	// Bridge: a neighbour announces bridge capability (a switch). Only those
	// count towards the uplink hint; PCs, phones and hypervisors often send
	// LLDP too.
	Bridge bool
}

// SightingOptions are the limits for RecordSightings.
type SightingOptions struct {
	// UplinkThreshold: more MACs than this on a port count towards the uplink
	// hint (polling.uplink_mac_threshold).
	UplinkThreshold int
	// MaxPortMACs: a port with more MACs than this in one read creates no new
	// devices (MAC flooding); known MACs are still updated. 0 means no limit.
	MaxPortMACs int
}

// RecordSightings stores where MACs were seen, updates the MAC count of every
// port and the uplink hint. It returns the ports that exceeded MaxPortMACs.
func (tx *Tx) RecordSightings(ctx context.Context, switchID int64, sightings []Sighting, lldp map[int64]PortNeighbor,
	opt SightingOptions, at time.Time) (flooded []int64, err error) {
	now := unix(at)
	counts := map[int64]map[string]bool{}
	for _, s := range sightings {
		if counts[s.PortID] == nil {
			counts[s.PortID] = map[string]bool{}
		}
		counts[s.PortID][s.MAC] = true
	}
	over := map[int64]bool{}
	if opt.MaxPortMACs > 0 {
		for portID, macs := range counts {
			if len(macs) > opt.MaxPortMACs {
				over[portID] = true
				flooded = append(flooded, portID)
			}
		}
		sort.Slice(flooded, func(i, j int) bool { return flooded[i] < flooded[j] })
	}
	for _, s := range sightings {
		if s.Uplink {
			continue
		}
		known, err := tx.ensureMAC(ctx, s.MAC, at, !over[s.PortID])
		if err != nil {
			return nil, err
		}
		if !known {
			continue // flooded port: no new MACs
		}
		if _, err := tx.tx.ExecContext(ctx, `INSERT INTO mac_locations (mac, port_id, vlan, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT (mac, port_id) DO UPDATE SET last_seen = excluded.last_seen, vlan = excluded.vlan`,
			s.MAC, s.PortID, s.VLAN, now, now); err != nil {
			return nil, err
		}
	}
	if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET mac_count = 0, lldp_neighbor = '' WHERE switch_id = ?`,
		switchID); err != nil {
		return nil, err
	}
	if _, err := tx.tx.ExecContext(ctx, `UPDATE switches SET fdb_read_at = ? WHERE id = ?`, now, switchID); err != nil {
		return nil, err
	}
	for portID, macs := range counts {
		if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET mac_count = ? WHERE id = ?`, len(macs), portID); err != nil {
			return nil, err
		}
	}
	// Ports that look like an uplink in this read: a bridge as LLDP neighbour
	// or more MACs than the threshold. The streak counts such reads in a row.
	looks := map[int64]bool{}
	for portID, macs := range counts {
		if len(macs) > opt.UplinkThreshold {
			looks[portID] = true
		}
	}
	for portID, n := range lldp {
		if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET lldp_neighbor = ? WHERE id = ?`, n.Name, portID); err != nil {
			return nil, err
		}
		if n.Bridge {
			looks[portID] = true
		}
	}
	if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET uplink_streak = -uplink_streak - 1 WHERE switch_id = ?`,
		switchID); err != nil {
		return nil, err
	}
	for portID := range looks {
		// Undo the marker above and count this read.
		if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET uplink_streak = -uplink_streak WHERE id = ?`, portID); err != nil {
			return nil, err
		}
	}
	// Ports that did not look like an uplink are still negative: reset them.
	if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET uplink_streak = 0 WHERE switch_id = ? AND uplink_streak < 0`,
		switchID); err != nil {
		return nil, err
	}
	// Ports already confirmed as uplink, or as not an uplink, need no hint.
	_, err = tx.tx.ExecContext(ctx, `UPDATE ports SET uplink_suspect = CASE
		WHEN role NOT IN ('uplink', 'wan') AND uplink_dismissed = 0 AND uplink_streak >= ? THEN 1 ELSE 0 END
		WHERE switch_id = ?`, UplinkConfirmReads, switchID)
	return flooded, err
}

// ensureMAC updates last_seen of a known MAC. An unknown MAC gets a new device
// when create is set. It reports whether the MAC exists afterwards.
func (tx *Tx) ensureMAC(ctx context.Context, mac string, at time.Time, create bool) (bool, error) {
	res, err := tx.tx.ExecContext(ctx, `UPDATE macs SET last_seen = ? WHERE mac = ?`, unix(at), mac)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return true, nil
	}
	if !create {
		return false, nil
	}
	dev, err := tx.tx.ExecContext(ctx, `INSERT INTO devices (created_at) VALUES (?)`, unix(at))
	if err != nil {
		return false, err
	}
	devID, err := dev.LastInsertId()
	if err != nil {
		return false, err
	}
	_, err = tx.tx.ExecContext(ctx, `INSERT INTO macs (mac, device_id, vendor, first_seen, last_seen) VALUES (?, ?, ?, ?, ?)`,
		mac, devID, oui.LookupString(mac), unix(at), unix(at))
	return err == nil, err
}

// RefreshVendors updates the vendor names of all MACs from the embedded OUI
// data (after an update of LANLeecher brought newer data). It returns the
// number of changed rows.
func (db *DB) RefreshVendors(ctx context.Context) (int, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT mac, vendor FROM macs`)
	if err != nil {
		return 0, err
	}
	changes := map[string]string{}
	for rows.Next() {
		var mac, vendor string
		if err := rows.Scan(&mac, &vendor); err != nil {
			rows.Close()
			return 0, err
		}
		if v := oui.LookupString(mac); v != vendor {
			changes[mac] = v
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(changes) == 0 {
		return 0, err
	}
	err = db.InTx(ctx, func(tx *Tx) error {
		for mac, v := range changes {
			if _, err := tx.tx.ExecContext(ctx, `UPDATE macs SET vendor = ? WHERE mac = ?`, v, mac); err != nil {
				return err
			}
		}
		return nil
	})
	return len(changes), err
}

// EnsureMAC makes sure a MAC exists (used by resolvers for MACs seen only via ARP).
func (db *DB) EnsureMAC(ctx context.Context, mac string, at time.Time) error {
	return db.InTx(ctx, func(tx *Tx) error { _, err := tx.ensureMAC(ctx, mac, at, true); return err })
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
	// Vendor from the IEEE OUI registry; empty for randomized MACs.
	Vendor   string
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
	rows, err := db.sql.QueryContext(ctx, `SELECT mac, device_id, vendor, last_seen FROM macs WHERE mac IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var m MACInfo
		var seen int64
		if err := rows.Scan(&m.MAC, &m.DeviceID, &m.Vendor, &seen); err != nil {
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

// MACsWithHostname returns up to limit MACs (other than exclude) that had hostname since a time,
// the most recently seen first.
func (db *DB) MACsWithHostname(ctx context.Context, hostname, exclude string, since time.Time, limit int) ([]string, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT mac FROM mac_hostname_history
		WHERE hostname = ? COLLATE NOCASE AND mac <> ? AND last_seen >= ?
		GROUP BY mac ORDER BY MAX(last_seen) DESC LIMIT ?`, hostname, exclude, unix(since), limit)
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

// PurgeIdleMACs deletes MACs that were last seen before the cutoff and never
// had any traffic: no traffic totals of their own and no port group they
// belonged to. Their location, IP and name history goes with them (ON DELETE
// CASCADE), open merge suggestions that mention them, and devices left
// without any MAC. MACs of devices an operator touched (display name, notes,
// hidden from rankings) are kept. It returns the number of deleted MACs.
func (db *DB) PurgeIdleMACs(ctx context.Context, cutoff time.Time) (int64, error) {
	var n int64
	err := db.InTx(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `DELETE FROM macs WHERE last_seen < ?
			AND NOT EXISTS (SELECT 1 FROM traffic_totals t WHERE t.subject_type = 'mac' AND t.subject_id = macs.mac)
			AND NOT EXISTS (SELECT 1 FROM port_group_macs g WHERE g.mac = macs.mac)
			AND device_id IN (SELECT id FROM devices WHERE display_name = '' AND notes = '' AND hidden_in_rankings = 0)`,
			unix(cutoff))
		if err != nil {
			return err
		}
		if n, err = res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM merge_suggestions WHERE status = 'open'
			AND (mac_a NOT IN (SELECT mac FROM macs) OR mac_b NOT IN (SELECT mac FROM macs))`); err != nil {
			return err
		}
		_, err = tx.tx.ExecContext(ctx, `DELETE FROM devices WHERE NOT EXISTS (SELECT 1 FROM macs WHERE macs.device_id = devices.id)
			AND display_name = '' AND notes = '' AND hidden_in_rankings = 0`)
		return err
	})
	return n, err
}

// MACOnOtherClientPort reports whether a MAC is currently also on another
// port that is not an uplink or WAN port (on any switch): the last
// forwarding-table read of that port's switch still saw it there, and that
// read is not older than since. A device that moved to another port stops
// counting as soon as its old switch is read again; a cloned MAC keeps
// counting while both are connected. Such traffic is credited to nobody.
func (tx *Tx) MACOnOtherClientPort(ctx context.Context, mac string, portID int64, since time.Time) (bool, error) {
	var n int
	err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mac_locations l
		JOIN ports p ON p.id = l.port_id JOIN switches s ON s.id = p.switch_id
		WHERE l.mac = ? AND l.port_id <> ? AND p.role NOT IN ('uplink', 'wan')
		AND s.fdb_read_at > 0 AND l.last_seen >= s.fdb_read_at AND l.last_seen >= ?`,
		mac, portID, unix(since)).Scan(&n)
	return n > 0, err
}
