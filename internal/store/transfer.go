package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/oui"
	"github.com/lan-dot-party/lanleecher/internal/transfer"
)

// ExportOptions selects what Export writes.
type ExportOptions struct {
	// Events limits the export to these slugs; empty exports all events.
	// With a filter, only the MACs (and their devices) that appear in the
	// selected events are exported.
	Events []string
	// Software and Source go into the file header.
	Software string
	Source   string
	Now      time.Time
}

// ExportStats counts rows that could not be exported because they point at
// deleted ports or unknown MACs (orphans of deleted switches).
type ExportStats struct {
	SkippedRows int
}

// Export reads switches, ports, devices, MACs and events into an export
// document. It never reads credentials. The reads run in one deferred
// transaction, so the snapshot is consistent and pollers are not blocked.
func (db *DB) Export(ctx context.Context, opts ExportOptions) (*transfer.Doc, ExportStats, error) {
	var stats ExportStats
	conn, err := db.sql.Conn(ctx)
	if err != nil {
		return nil, stats, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return nil, stats, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK") //nolint:errcheck // read-only snapshot

	now := opts.Now
	if now.IsZero() {
		now = Now()
	}
	doc := &transfer.Doc{Format: transfer.Format, Version: transfer.Version, ExportedAt: unix(now),
		Software: opts.Software, Source: opts.Source}

	// Switches and ports.
	switchName := map[int64]string{}
	swIndex := map[int64]int{}
	rows, err := conn.QueryContext(ctx, `SELECT id, name, host, port, sys_name, sys_descr, fdb_adapter, created_at
		FROM switches ORDER BY name`)
	if err != nil {
		return nil, stats, err
	}
	for rows.Next() {
		var id int64
		var s transfer.Switch
		if err := rows.Scan(&id, &s.Name, &s.Host, &s.Port, &s.SysName, &s.SysDescr, &s.FDBAdapter, &s.CreatedAt); err != nil {
			rows.Close()
			return nil, stats, err
		}
		switchName[id] = s.Name
		swIndex[id] = len(doc.Switches)
		doc.Switches = append(doc.Switches, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, stats, err
	}

	portRef := map[int64]transfer.PortRef{}
	rows, err = conn.QueryContext(ctx, `SELECT id, switch_id, if_index, if_name, if_alias, if_descr, if_type, physical,
		speed_mbps, role, label, hidden_in_rankings, uplink_dismissed, last_seen FROM ports ORDER BY switch_id, if_index`)
	if err != nil {
		return nil, stats, err
	}
	for rows.Next() {
		var id, swID int64
		var p transfer.Port
		if err := rows.Scan(&id, &swID, &p.IfIndex, &p.IfName, &p.IfAlias, &p.IfDescr, &p.IfType, &p.Physical,
			&p.SpeedMbps, &p.Role, &p.Label, &p.Hidden, &p.UplinkDismissed, &p.LastSeen); err != nil {
			rows.Close()
			return nil, stats, err
		}
		i, ok := swIndex[swID]
		if !ok || p.IfIndex <= 0 {
			continue
		}
		doc.Switches[i].Ports = append(doc.Switches[i].Ports, p)
		portRef[id] = transfer.PortRef{Switch: switchName[swID], IfIndex: p.IfIndex}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, stats, err
	}

	// All known MACs (needed to drop traffic rows of unknown MACs).
	knownMAC := map[string]bool{}
	if err := queryEach(ctx, conn, `SELECT mac FROM macs`, nil, func(r *sql.Rows) error {
		var m string
		if err := r.Scan(&m); err != nil {
			return err
		}
		knownMAC[m] = true
		return nil
	}); err != nil {
		return nil, stats, err
	}

	// Events.
	want := map[string]bool{}
	for _, s := range opts.Events {
		want[s] = true
	}
	type evRow struct {
		id int64
		ev transfer.Event
	}
	var events []evRow
	if err := queryEach(ctx, conn, `SELECT id, slug, name, notes, started_at, ended_at FROM events ORDER BY started_at, id`, nil,
		func(r *sql.Rows) error {
			var e evRow
			var ended sql.NullInt64
			if err := r.Scan(&e.id, &e.ev.Slug, &e.ev.Name, &e.ev.Notes, &e.ev.StartedAt, &ended); err != nil {
				return err
			}
			if ended.Valid {
				v := ended.Int64
				e.ev.EndedAt = &v
			}
			if len(want) == 0 || want[e.ev.Slug] {
				events = append(events, e)
			}
			return nil
		}); err != nil {
		return nil, stats, err
	}
	if len(want) > 0 && len(events) != len(want) {
		found := map[string]bool{}
		for _, e := range events {
			found[e.ev.Slug] = true
		}
		for s := range want {
			if !found[s] {
				return nil, stats, fmt.Errorf("event %q: %w", s, ErrNotFound)
			}
		}
	}

	usedMAC := map[string]bool{}
	for i := range events {
		e := &events[i]
		if err := queryEach(ctx, conn, `SELECT port_id, bytes_down, bytes_up, updated_at FROM port_traffic_totals
			WHERE event_id = ? ORDER BY port_id`, []any{e.id}, func(r *sql.Rows) error {
			var pid int64
			var t transfer.PortTotal
			if err := r.Scan(&pid, &t.Down, &t.Up, &t.UpdatedAt); err != nil {
				return err
			}
			ref, ok := portRef[pid]
			if !ok {
				stats.SkippedRows++
				return nil
			}
			t.Port = ref
			e.ev.PortTotals = append(e.ev.PortTotals, t)
			return nil
		}); err != nil {
			return nil, stats, err
		}

		groups := map[string]int{}
		if err := queryEach(ctx, conn, `SELECT id, port_id FROM port_groups WHERE event_id = ? ORDER BY id`, []any{e.id},
			func(r *sql.Rows) error {
				var gid, pid int64
				if err := r.Scan(&gid, &pid); err != nil {
					return err
				}
				ref, ok := portRef[pid]
				if !ok {
					stats.SkippedRows++
					return nil
				}
				key := strconv.FormatInt(gid, 10)
				groups[key] = len(e.ev.PortGroups)
				e.ev.PortGroups = append(e.ev.PortGroups, transfer.PortGroup{Key: key, Port: ref})
				return nil
			}); err != nil {
			return nil, stats, err
		}
		if err := queryEach(ctx, conn, `SELECT m.group_id, m.mac, m.first_seen, m.last_seen FROM port_group_macs m
			JOIN port_groups g ON g.id = m.group_id WHERE g.event_id = ? ORDER BY m.group_id, m.mac`, []any{e.id},
			func(r *sql.Rows) error {
				var gid int64
				var gm transfer.GroupMAC
				if err := r.Scan(&gid, &gm.MAC, &gm.FirstSeen, &gm.LastSeen); err != nil {
					return err
				}
				gi, ok := groups[strconv.FormatInt(gid, 10)]
				if !ok {
					return nil
				}
				e.ev.PortGroups[gi].MACs = append(e.ev.PortGroups[gi].MACs, gm)
				if knownMAC[gm.MAC] {
					usedMAC[gm.MAC] = true
				}
				return nil
			}); err != nil {
			return nil, stats, err
		}

		convert := func(typ, subject string, pid int64) (transfer.Traffic, bool) {
			ref, ok := portRef[pid]
			if !ok {
				return transfer.Traffic{}, false
			}
			t := transfer.Traffic{Type: typ, Port: ref}
			switch typ {
			case SubjectMAC:
				if !knownMAC[subject] {
					return t, false
				}
				usedMAC[subject] = true
				t.Subject = subject
			case SubjectPortGroup:
				if _, ok := groups[subject]; !ok {
					return t, false
				}
				t.Subject = subject
			case SubjectUnassigned:
			default:
				return t, false
			}
			return t, true
		}
		if err := queryEach(ctx, conn, `SELECT subject_type, subject_id, port_id, bytes_down, bytes_up, updated_at
			FROM traffic_totals WHERE event_id = ? ORDER BY subject_type, subject_id, port_id`, []any{e.id},
			func(r *sql.Rows) error {
				var typ, subj string
				var pid, down, up, upd int64
				if err := r.Scan(&typ, &subj, &pid, &down, &up, &upd); err != nil {
					return err
				}
				t, ok := convert(typ, subj, pid)
				if !ok {
					stats.SkippedRows++
					return nil
				}
				t.Down, t.Up, t.UpdatedAt = down, up, upd
				e.ev.Totals = append(e.ev.Totals, t)
				return nil
			}); err != nil {
			return nil, stats, err
		}
		if err := queryEach(ctx, conn, `SELECT subject_type, subject_id, port_id, bucket_start, bytes_down, bytes_up
			FROM traffic_buckets WHERE event_id = ? ORDER BY bucket_start, subject_type, subject_id, port_id`, []any{e.id},
			func(r *sql.Rows) error {
				var typ, subj string
				var pid, start, down, up int64
				if err := r.Scan(&typ, &subj, &pid, &start, &down, &up); err != nil {
					return err
				}
				t, ok := convert(typ, subj, pid)
				if !ok {
					stats.SkippedRows++
					return nil
				}
				t.Start, t.Down, t.Up = start, down, up
				e.ev.Buckets = append(e.ev.Buckets, t)
				return nil
			}); err != nil {
			return nil, stats, err
		}
		doc.Events = append(doc.Events, e.ev)
	}

	// MACs and devices. With an event filter only the MACs used there, plus
	// the other MACs of their devices.
	type macRow struct {
		m   transfer.MAC
		dev int64
	}
	var macs []macRow
	if err := queryEach(ctx, conn, `SELECT mac, device_id, vendor, first_seen, last_seen FROM macs ORDER BY mac`, nil,
		func(r *sql.Rows) error {
			var mr macRow
			if err := r.Scan(&mr.m.MAC, &mr.dev, &mr.m.Vendor, &mr.m.FirstSeen, &mr.m.LastSeen); err != nil {
				return err
			}
			macs = append(macs, mr)
			return nil
		}); err != nil {
		return nil, stats, err
	}
	keepDev := map[int64]bool{}
	for _, mr := range macs {
		if len(want) == 0 || usedMAC[mr.m.MAC] {
			keepDev[mr.dev] = true
		}
	}
	macIndex := map[string]int{}
	var exported []transfer.MAC
	for _, mr := range macs {
		if !keepDev[mr.dev] {
			continue
		}
		mr.m.Device = strconv.FormatInt(mr.dev, 10)
		macIndex[mr.m.MAC] = len(exported)
		exported = append(exported, mr.m)
	}
	if err := queryEach(ctx, conn, `SELECT mac, port_id, vlan, first_seen, last_seen FROM mac_locations ORDER BY mac, port_id`, nil,
		func(r *sql.Rows) error {
			var mac string
			var pid int64
			var l transfer.Location
			if err := r.Scan(&mac, &pid, &l.VLAN, &l.FirstSeen, &l.LastSeen); err != nil {
				return err
			}
			i, ok := macIndex[mac]
			ref, pok := portRef[pid]
			if !ok || !pok {
				return nil
			}
			l.Port = ref
			exported[i].Locations = append(exported[i].Locations, l)
			return nil
		}); err != nil {
		return nil, stats, err
	}
	for _, h := range []struct {
		query string
		ips   bool
	}{
		{`SELECT mac, ip, source, first_seen, last_seen FROM mac_ip_history ORDER BY mac, ip, source`, true},
		{`SELECT mac, hostname, source, first_seen, last_seen FROM mac_hostname_history ORDER BY mac, hostname, source`, false},
	} {
		if err := queryEach(ctx, conn, h.query, nil, func(r *sql.Rows) error {
			var mac string
			var e transfer.History
			if err := r.Scan(&mac, &e.Value, &e.Source, &e.FirstSeen, &e.LastSeen); err != nil {
				return err
			}
			i, ok := macIndex[mac]
			if !ok {
				return nil
			}
			if h.ips {
				exported[i].IPs = append(exported[i].IPs, e)
			} else {
				exported[i].Hostnames = append(exported[i].Hostnames, e)
			}
			return nil
		}); err != nil {
			return nil, stats, err
		}
	}
	doc.MACs = exported
	if err := queryEach(ctx, conn, `SELECT id, display_name, hidden_in_rankings, notes, created_at FROM devices ORDER BY id`, nil,
		func(r *sql.Rows) error {
			var id int64
			var d transfer.Device
			if err := r.Scan(&id, &d.DisplayName, &d.Hidden, &d.Notes, &d.CreatedAt); err != nil {
				return err
			}
			if keepDev[id] {
				d.Key = strconv.FormatInt(id, 10)
				doc.Devices = append(doc.Devices, d)
			}
			return nil
		}); err != nil {
		return nil, stats, err
	}
	return doc, stats, nil
}

func queryEach(ctx context.Context, q querier, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Event conflict policies for Import.
const (
	// ConflictNew imports an event whose slug exists under a new slug.
	ConflictNew = "new"
	// ConflictSkip leaves such an event out.
	ConflictSkip = "skip"
)

// ImportOptions control Import.
type ImportOptions struct {
	// EventConflict is ConflictNew (default) or ConflictSkip.
	EventConflict string
	// DryRun runs the whole import and rolls it back; the report shows
	// exactly what a real import would do.
	DryRun bool
	Now    time.Time
}

// Import actions in the report.
const (
	ActionMapped   = "mapped"   // switch exists here (same name and host)
	ActionArchived = "archived" // created as an archived switch
	ActionRenamed  = "renamed"  // archived under another name (name taken by a different host)
	ActionImported = "imported" // event imported under its own slug
	ActionSkipped  = "skipped"  // event left out (slug taken, policy skip)
)

// ImportReport describes what an import did (or would do).
type ImportReport struct {
	DryRun     bool
	Switches   []SwitchImport
	DevicesNew int
	MACsNew    int
	MACsMerged int
	Events     []EventImport
	// TrafficRows is the number of total and bucket rows written.
	TrafficRows int
}

// SwitchImport is the outcome for one switch of the file.
type SwitchImport struct {
	Name       string
	Host       string
	Action     string
	TargetName string
	PortsNew   int
}

// EventImport is the outcome for one event of the file.
type EventImport struct {
	Slug       string
	Name       string
	Action     string // imported, renamed, skipped
	TargetSlug string
	// Closed is set when the event was still running in the source; it is
	// imported as ended at its last recorded traffic.
	Closed bool
}

var errDryRun = errors.New("dry run")

// Import writes an export document into this database in one transaction.
// Run Backup before a real import. The document must have passed Validate
// (transfer.Read does that).
func (db *DB) Import(ctx context.Context, doc *transfer.Doc, opts ImportOptions) (*ImportReport, error) {
	if opts.EventConflict == "" {
		opts.EventConflict = ConflictNew
	}
	if opts.EventConflict != ConflictNew && opts.EventConflict != ConflictSkip {
		return nil, fmt.Errorf("unknown event conflict policy %q", opts.EventConflict)
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	now := opts.Now
	if now.IsZero() {
		now = Now()
	}
	var rep *ImportReport
	err := db.InTx(ctx, func(tx *Tx) error {
		r, err := tx.importDoc(ctx, doc, opts, unix(now))
		if err != nil {
			return err
		}
		rep = r
		if opts.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	rep.DryRun = opts.DryRun
	return rep, nil
}

func (tx *Tx) importDoc(ctx context.Context, doc *transfer.Doc, opts ImportOptions, now int64) (*ImportReport, error) {
	t := tx.tx
	rep := &ImportReport{}

	// Switches: map onto an existing switch with the same name and host,
	// otherwise create an archived one.
	portID := map[transfer.PortRef]int64{}
	for _, s := range doc.Switches {
		res := SwitchImport{Name: s.Name, Host: s.Host}
		var id int64
		var host string
		err := t.QueryRowContext(ctx, `SELECT id, host FROM switches WHERE name = ?`, s.Name).Scan(&id, &host)
		switch {
		case err == nil && host == s.Host:
			res.Action, res.TargetName = ActionMapped, s.Name
		case err == nil || errors.Is(err, sql.ErrNoRows):
			name := s.Name
			res.Action = ActionArchived
			if err == nil {
				res.Action = ActionRenamed
				if name, err = freeSwitchName(ctx, t, s.Name); err != nil {
					return nil, err
				}
			}
			res.TargetName = name
			created := s.CreatedAt
			if created <= 0 {
				created = now
			}
			ins, err := t.ExecContext(ctx, `INSERT INTO switches (name, host, port, credentials_enc, fdb_adapter,
				enabled, archived, sys_descr, sys_name, created_at) VALUES (?, ?, ?, x'', ?, 0, 1, ?, ?, ?)`,
				name, s.Host, defaultInt(s.Port, 161), defaultStr(s.FDBAdapter, "auto"), s.SysDescr, s.SysName, created)
			if err != nil {
				return nil, fmt.Errorf("switch %q: %w", s.Name, err)
			}
			if id, err = ins.LastInsertId(); err != nil {
				return nil, err
			}
		default:
			return nil, err
		}
		for _, p := range s.Ports {
			var pid int64
			err := t.QueryRowContext(ctx, `SELECT id FROM ports WHERE switch_id = ? AND if_index = ?`, id, p.IfIndex).Scan(&pid)
			if errors.Is(err, sql.ErrNoRows) {
				ins, err := t.ExecContext(ctx, `INSERT INTO ports (switch_id, if_index, if_name, if_alias, if_descr, if_type,
					physical, speed_mbps, role, label, hidden_in_rankings, uplink_dismissed, last_seen)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					id, p.IfIndex, p.IfName, p.IfAlias, p.IfDescr, p.IfType, boolInt(p.Physical), int64(p.SpeedMbps),
					p.Role, p.Label, boolInt(p.Hidden), boolInt(p.UplinkDismissed), p.LastSeen)
				if err != nil {
					return nil, fmt.Errorf("switch %q port %d: %w", s.Name, p.IfIndex, err)
				}
				if pid, err = ins.LastInsertId(); err != nil {
					return nil, err
				}
				res.PortsNew++
			} else if err != nil {
				return nil, err
			}
			portID[transfer.PortRef{Switch: s.Name, IfIndex: p.IfIndex}] = pid
		}
		rep.Switches = append(rep.Switches, res)
	}

	// Devices and MACs. An existing MAC keeps its device here; new MACs join
	// the device of an existing MAC of the same source device, or a new one.
	devByKey := map[string]transfer.Device{}
	for _, d := range doc.Devices {
		devByKey[d.Key] = d
	}
	macsByDev := map[string][]transfer.MAC{}
	var devOrder []string
	for _, m := range doc.MACs {
		if _, seen := macsByDev[m.Device]; !seen {
			devOrder = append(devOrder, m.Device)
		}
		macsByDev[m.Device] = append(macsByDev[m.Device], m)
	}
	for _, key := range devOrder {
		members := macsByDev[key]
		src := devByKey[key]
		existing := map[string]int64{}
		var anchor int64
		for _, m := range members {
			var dev int64
			err := t.QueryRowContext(ctx, `SELECT device_id FROM macs WHERE mac = ?`, m.MAC).Scan(&dev)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			existing[m.MAC] = dev
			if anchor == 0 || dev < anchor {
				anchor = dev
			}
		}
		if anchor == 0 {
			created := src.CreatedAt
			if created <= 0 {
				created = now
			}
			ins, err := t.ExecContext(ctx, `INSERT INTO devices (display_name, hidden_in_rankings, notes, created_at)
				VALUES (?, ?, ?, ?)`, src.DisplayName, boolInt(src.Hidden), src.Notes, created)
			if err != nil {
				return nil, err
			}
			if anchor, err = ins.LastInsertId(); err != nil {
				return nil, err
			}
			rep.DevicesNew++
		} else if src.DisplayName != "" {
			if _, err := t.ExecContext(ctx, `UPDATE devices SET display_name = ? WHERE id = ? AND display_name = ''`,
				src.DisplayName, anchor); err != nil {
				return nil, err
			}
		}
		for _, m := range members {
			if _, ok := existing[m.MAC]; ok {
				if _, err := t.ExecContext(ctx, `UPDATE macs SET first_seen = min(first_seen, ?), last_seen = max(last_seen, ?),
					vendor = CASE WHEN vendor = '' THEN ? ELSE vendor END WHERE mac = ?`,
					m.FirstSeen, m.LastSeen, m.Vendor, m.MAC); err != nil {
					return nil, err
				}
				rep.MACsMerged++
			} else {
				vendor := m.Vendor
				if vendor == "" {
					vendor = oui.LookupString(m.MAC)
				}
				if _, err := t.ExecContext(ctx, `INSERT INTO macs (mac, device_id, vendor, first_seen, last_seen)
					VALUES (?, ?, ?, ?, ?)`, m.MAC, anchor, vendor, m.FirstSeen, m.LastSeen); err != nil {
					return nil, err
				}
				rep.MACsNew++
			}
			for _, l := range m.Locations {
				if _, err := t.ExecContext(ctx, `INSERT INTO mac_locations (mac, port_id, vlan, first_seen, last_seen)
					VALUES (?, ?, ?, ?, ?) ON CONFLICT (mac, port_id) DO UPDATE SET
					first_seen = min(first_seen, excluded.first_seen), last_seen = max(last_seen, excluded.last_seen)`,
					m.MAC, portID[l.Port], l.VLAN, l.FirstSeen, l.LastSeen); err != nil {
					return nil, err
				}
			}
			for _, h := range m.IPs {
				if _, err := t.ExecContext(ctx, `INSERT INTO mac_ip_history (mac, ip, source, first_seen, last_seen)
					VALUES (?, ?, ?, ?, ?) ON CONFLICT (mac, ip, source) DO UPDATE SET
					first_seen = min(first_seen, excluded.first_seen), last_seen = max(last_seen, excluded.last_seen)`,
					m.MAC, h.Value, h.Source, h.FirstSeen, h.LastSeen); err != nil {
					return nil, err
				}
			}
			for _, h := range m.Hostnames {
				if _, err := t.ExecContext(ctx, `INSERT INTO mac_hostname_history (mac, hostname, source, first_seen, last_seen)
					VALUES (?, ?, ?, ?, ?) ON CONFLICT (mac, hostname, source) DO UPDATE SET
					first_seen = min(first_seen, excluded.first_seen), last_seen = max(last_seen, excluded.last_seen)`,
					m.MAC, h.Value, h.Source, h.FirstSeen, h.LastSeen); err != nil {
					return nil, err
				}
			}
		}
	}

	// Events: always imported as ended events with new IDs.
	for _, e := range doc.Events {
		res := EventImport{Slug: e.Slug, Name: e.Name, Action: ActionImported, TargetSlug: e.Slug}
		var n int
		if err := t.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE slug = ?`, e.Slug).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			if opts.EventConflict == ConflictSkip {
				res.Action = ActionSkipped
				res.TargetSlug = ""
				rep.Events = append(rep.Events, res)
				continue
			}
			slug, err := freeSlug(ctx, t, e.Slug)
			if err != nil {
				return nil, err
			}
			res.Action, res.TargetSlug = ActionRenamed, slug
		}
		ended := lastActivity(e)
		if e.EndedAt != nil {
			ended = *e.EndedAt
		} else {
			res.Closed = true
			if ended > now {
				ended = now
			}
		}
		ins, err := t.ExecContext(ctx, `INSERT INTO events (name, slug, started_at, ended_at, notes) VALUES (?, ?, ?, ?, ?)`,
			e.Name, res.TargetSlug, e.StartedAt, ended, e.Notes)
		if err != nil {
			return nil, fmt.Errorf("event %q: %w", e.Slug, err)
		}
		eventID, err := ins.LastInsertId()
		if err != nil {
			return nil, err
		}
		for _, pt := range e.PortTotals {
			if _, err := t.ExecContext(ctx, `INSERT INTO port_traffic_totals (event_id, port_id, bytes_down, bytes_up, updated_at)
				VALUES (?, ?, ?, ?, ?) ON CONFLICT (event_id, port_id) DO UPDATE SET
				bytes_down = bytes_down + excluded.bytes_down, bytes_up = bytes_up + excluded.bytes_up`,
				eventID, portID[pt.Port], pt.Down, pt.Up, pt.UpdatedAt); err != nil {
				return nil, err
			}
		}
		groupID := map[string]int64{}
		for _, g := range e.PortGroups {
			pid := portID[g.Port]
			if _, err := t.ExecContext(ctx, `INSERT INTO port_groups (event_id, port_id) VALUES (?, ?)
				ON CONFLICT (event_id, port_id) DO NOTHING`, eventID, pid); err != nil {
				return nil, err
			}
			var gid int64
			if err := t.QueryRowContext(ctx, `SELECT id FROM port_groups WHERE event_id = ? AND port_id = ?`,
				eventID, pid).Scan(&gid); err != nil {
				return nil, err
			}
			groupID[g.Key] = gid
			for _, m := range g.MACs {
				if _, err := t.ExecContext(ctx, `INSERT INTO port_group_macs (group_id, mac, first_seen, last_seen)
					VALUES (?, ?, ?, ?) ON CONFLICT (group_id, mac) DO UPDATE SET
					first_seen = min(first_seen, excluded.first_seen), last_seen = max(last_seen, excluded.last_seen)`,
					gid, m.MAC, m.FirstSeen, m.LastSeen); err != nil {
					return nil, err
				}
			}
		}
		subject := func(tr transfer.Traffic) (string, int64) {
			pid := portID[tr.Port]
			switch tr.Type {
			case transfer.SubjectPortGroup:
				return strconv.FormatInt(groupID[tr.Subject], 10), pid
			case transfer.SubjectUnassigned:
				return strconv.FormatInt(pid, 10), pid
			}
			return tr.Subject, pid
		}
		for _, tr := range e.Totals {
			subj, pid := subject(tr)
			upd := tr.UpdatedAt
			if upd <= 0 {
				upd = ended
			}
			if _, err := t.ExecContext(ctx, `INSERT INTO traffic_totals
				(event_id, subject_type, subject_id, port_id, bytes_down, bytes_up, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (event_id, subject_type, subject_id, port_id) DO UPDATE SET
				bytes_down = bytes_down + excluded.bytes_down, bytes_up = bytes_up + excluded.bytes_up`,
				eventID, tr.Type, subj, pid, tr.Down, tr.Up, upd); err != nil {
				return nil, err
			}
			rep.TrafficRows++
		}
		for _, tr := range e.Buckets {
			subj, pid := subject(tr)
			start := time.Unix(tr.Start, 0).UTC().Truncate(BucketSize).Unix()
			if _, err := t.ExecContext(ctx, `INSERT INTO traffic_buckets
				(event_id, subject_type, subject_id, port_id, bucket_start, bytes_down, bytes_up) VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (event_id, subject_type, subject_id, port_id, bucket_start) DO UPDATE SET
				bytes_down = bytes_down + excluded.bytes_down, bytes_up = bytes_up + excluded.bytes_up`,
				eventID, tr.Type, subj, pid, start, tr.Down, tr.Up); err != nil {
				return nil, err
			}
			rep.TrafficRows++
		}
		rep.Events = append(rep.Events, res)
	}
	sort.SliceStable(rep.Switches, func(i, j int) bool { return rep.Switches[i].Name < rep.Switches[j].Name })
	return rep, nil
}

// lastActivity is the end of the last bucket or the latest total update of
// an event, at least its start.
func lastActivity(e transfer.Event) int64 {
	last := e.StartedAt
	for _, b := range e.Buckets {
		if end := b.Start + int64(BucketSize/time.Second); end > last {
			last = end
		}
	}
	for _, t := range e.Totals {
		if t.UpdatedAt > last {
			last = t.UpdatedAt
		}
	}
	for _, t := range e.PortTotals {
		if t.UpdatedAt > last {
			last = t.UpdatedAt
		}
	}
	return last
}

func freeSlug(ctx context.Context, t *sql.Tx, base string) (string, error) {
	if len(base) > 70 {
		base = base[:70]
	}
	for i := 1; i < 1000; i++ {
		slug := base + "-import"
		if i > 1 {
			slug += "-" + strconv.Itoa(i)
		}
		var n int
		if err := t.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE slug = ?`, slug).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return slug, nil
		}
	}
	return "", fmt.Errorf("no free slug for %q", base)
}

func freeSwitchName(ctx context.Context, t *sql.Tx, base string) (string, error) {
	for i := 1; i < 1000; i++ {
		name := base + " (import)"
		if i > 1 {
			name = fmt.Sprintf("%s (import %d)", base, i)
		}
		var n int
		if err := t.QueryRowContext(ctx, `SELECT COUNT(*) FROM switches WHERE name = ?`, name).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return name, nil
		}
	}
	return "", fmt.Errorf("no free switch name for %q", base)
}

func defaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
