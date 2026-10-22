package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

// RankingQuery selects a ranking.
type RankingQuery struct {
	// EventID selects one event; 0 ranks across all events.
	EventID int64
	// SwitchID restricts to one switch; 0 means all.
	SwitchID int64
	// From/To restrict to a time range using 5-minute buckets (optional).
	From, To time.Time
	// Order is "down", "up" or "total".
	Order string
	Limit int
	// IncludeHidden adds hidden ports and devices (marked Hidden).
	IncludeHidden bool
	// HiddenOnly returns only hidden entries (the "Ausgeblendet" list).
	HiddenOnly bool
}

// RankEntry is one line of a ranking.
type RankEntry struct {
	Rank int
	// Type is "mac" (a device, possibly with several MACs) or "port_group".
	Type string
	// Key is the device ID for "mac" or the port ID for "port_group".
	Key       int64
	Name      string
	BytesDown int64
	BytesUp   int64
	Hidden    bool
	MACs      []string
	IP        string
	Hostname  string
}

// Total returns down + up.
func (r RankEntry) Total() int64 { return r.BytesDown + r.BytesUp }

// Rankings returns the top entries. Visibility is filtered at query time by
// joining ports and devices, so hiding or unhiding applies retroactively.
func (db *DB) Rankings(ctx context.Context, q RankingQuery) ([]RankEntry, error) {
	src := "traffic_totals"
	var where []string
	var args []any
	if !q.From.IsZero() || !q.To.IsZero() {
		src = "traffic_buckets"
		if !q.From.IsZero() {
			where = append(where, "t.bucket_start >= ?")
			args = append(args, unix(q.From.Truncate(BucketSize)))
		}
		if !q.To.IsZero() {
			where = append(where, "t.bucket_start < ?")
			args = append(args, unix(q.To))
		}
	}
	where = append(where, "t.subject_type IN ('mac', 'port_group')")
	if q.EventID != 0 {
		where = append(where, "t.event_id = ?")
		args = append(args, q.EventID)
	}
	if q.SwitchID != 0 {
		where = append(where, "p.switch_id = ?")
		args = append(args, q.SwitchID)
	}
	const hiddenExpr = "(p.hidden_in_rankings = 1 OR COALESCE(d.hidden_in_rankings, 0) = 1)"
	switch {
	case q.HiddenOnly:
		where = append(where, hiddenExpr)
	case !q.IncludeHidden:
		where = append(where, "NOT "+hiddenExpr)
	}
	order := "SUM(down) DESC"
	switch q.Order {
	case "up":
		order = "SUM(up) DESC"
	case "total":
		order = "SUM(down) + SUM(up) DESC"
	}
	if q.Limit <= 0 {
		q.Limit = 10
	}
	query := `SELECT stype, skey, SUM(down), SUM(up), MAX(hidden) FROM (
			SELECT CASE WHEN t.subject_type = 'mac' THEN 'mac' ELSE 'port_group' END AS stype,
				CASE WHEN t.subject_type = 'mac' THEN m.device_id ELSE t.port_id END AS skey,
				t.bytes_down AS down, t.bytes_up AS up,
				CASE WHEN ` + hiddenExpr + ` THEN 1 ELSE 0 END AS hidden
			FROM ` + src + ` t
			JOIN ports p ON p.id = t.port_id
			LEFT JOIN macs m ON t.subject_type = 'mac' AND m.mac = t.subject_id
			LEFT JOIN devices d ON d.id = m.device_id
			WHERE ` + strings.Join(where, " AND ") + `
		) GROUP BY stype, skey HAVING SUM(down) + SUM(up) > 0 ORDER BY ` + order + `, stype, skey LIMIT ?`
	args = append(args, q.Limit)

	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var out []RankEntry
	var deviceIDs, portIDs []int64
	for rows.Next() {
		var e RankEntry
		if err := rows.Scan(&e.Type, &e.Key, &e.BytesDown, &e.BytesUp, &e.Hidden); err != nil {
			rows.Close()
			return nil, err
		}
		e.Rank = len(out) + 1
		if e.Type == "mac" {
			deviceIDs = append(deviceIDs, e.Key)
		} else {
			portIDs = append(portIDs, e.Key)
		}
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	devices, err := db.DeviceLabels(ctx, deviceIDs)
	if err != nil {
		return nil, err
	}
	groups, err := db.portGroupLabels(ctx, portIDs, q.EventID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		e := &out[i]
		if e.Type == "mac" {
			l := devices[e.Key]
			e.Name, e.MACs, e.IP, e.Hostname = l.Name, l.MACs, l.IP, l.Hostname
		} else {
			l := groups[e.Key]
			e.Name, e.MACs = l.Name, l.MACs
		}
	}
	return out, nil
}

// Label is the display information of a device or port group.
type Label struct {
	Name     string
	MACs     []string
	IP       string
	Hostname string
}

// DeviceLabels returns display labels for devices: display name, else the
// preferred hostname, else the first MAC.
func (db *DB) DeviceLabels(ctx context.Context, ids []int64) (map[int64]Label, error) {
	out := map[int64]Label{}
	if len(ids) == 0 {
		return out, nil
	}
	ph, args := inListInt(ids)
	rows, err := db.sql.QueryContext(ctx, `SELECT d.id, d.display_name, m.mac FROM devices d
		JOIN macs m ON m.device_id = d.id WHERE d.id IN (`+ph+`) ORDER BY m.last_seen DESC`, args...)
	if err != nil {
		return nil, err
	}
	var allMACs []string
	names := map[int64]string{}
	for rows.Next() {
		var id int64
		var name, mac string
		if err := rows.Scan(&id, &name, &mac); err != nil {
			rows.Close()
			return nil, err
		}
		l := out[id]
		l.MACs = append(l.MACs, mac)
		out[id] = l
		names[id] = name
		allMACs = append(allMACs, mac)
	}
	rows.Close()
	infos, err := db.MACInfos(ctx, allMACs)
	if err != nil {
		return nil, err
	}
	for id, l := range out {
		// MACs are ordered newest first; take names from the newest that has one.
		for _, mac := range l.MACs {
			if l.Hostname == "" {
				l.Hostname = infos[mac].Hostname
			}
			if l.IP == "" {
				l.IP = infos[mac].IP
			}
		}
		switch {
		case names[id] != "":
			l.Name = names[id]
		case l.Hostname != "":
			l.Name = l.Hostname
		case len(l.MACs) > 0:
			l.Name = l.MACs[0]
		}
		out[id] = l
	}
	return out, nil
}

func (db *DB) portGroupLabels(ctx context.Context, portIDs []int64, eventID int64) (map[int64]Label, error) {
	out := map[int64]Label{}
	if len(portIDs) == 0 {
		return out, nil
	}
	ph, args := inListInt(portIDs)
	rows, err := db.sql.QueryContext(ctx, `SELECT `+portCols+` FROM ports p JOIN switches s ON s.id = p.switch_id
		WHERE p.id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		p, err := scanPort(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out[p.ID] = Label{Name: p.Name()}
	}
	rows.Close()

	query := `SELECT g.port_id, m.mac FROM port_group_macs m JOIN port_groups g ON g.id = m.group_id
		WHERE g.port_id IN (` + ph + `)`
	if eventID != 0 {
		query += ` AND g.event_id = ?`
		args = append(args, eventID)
	}
	rows, err = db.sql.QueryContext(ctx, query+` ORDER BY m.mac`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[int64]map[string]bool{}
	for rows.Next() {
		var id int64
		var mac string
		if err := rows.Scan(&id, &mac); err != nil {
			return nil, err
		}
		if seen[id] == nil {
			seen[id] = map[string]bool{}
		}
		if seen[id][mac] {
			continue
		}
		seen[id][mac] = true
		l := out[id]
		l.MACs = append(l.MACs, mac)
		out[id] = l
	}
	return out, rows.Err()
}

// SearchHit is a device found by MAC, IP or hostname.
type SearchHit struct {
	DeviceID int64
	Name     string
	MAC      string
	IP       string
	Hostname string
	LastSeen time.Time
	// Match says which field matched: mac, ip or hostname.
	Match string
}

// Search finds MACs by partial MAC (any notation), IP or hostname.
func (db *DB) Search(ctx context.Context, q string, limit int) ([]SearchHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	match := map[string]string{}
	add := func(query, kind string, args ...any) error {
		rows, err := db.sql.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var mac string
			if err := rows.Scan(&mac); err != nil {
				return err
			}
			if _, ok := match[mac]; !ok {
				match[mac] = kind
			}
		}
		return rows.Err()
	}
	like := "%" + escapeLike(q) + "%"
	if hex, ok := looksLikeMAC(q); ok {
		if err := add(`SELECT mac FROM macs WHERE REPLACE(mac, ':', '') LIKE ? LIMIT 200`, "mac", "%"+hex+"%"); err != nil {
			return nil, err
		}
	}
	if err := add(`SELECT DISTINCT mac FROM mac_ip_history WHERE ip LIKE ? ESCAPE '\' LIMIT 200`, "ip", like); err != nil {
		return nil, err
	}
	if err := add(`SELECT DISTINCT mac FROM mac_hostname_history WHERE hostname LIKE ? ESCAPE '\' LIMIT 200`, "hostname", like); err != nil {
		return nil, err
	}
	macs := make([]string, 0, len(match))
	for mac := range match {
		macs = append(macs, mac)
	}
	infos, err := db.MACInfos(ctx, macs)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, i := range infos {
		ids = append(ids, i.DeviceID)
	}
	labels, err := db.DeviceLabels(ctx, ids)
	if err != nil {
		return nil, err
	}
	var out []SearchHit
	for mac, kind := range match {
		i, ok := infos[mac]
		if !ok {
			continue
		}
		out = append(out, SearchHit{DeviceID: i.DeviceID, Name: labels[i.DeviceID].Name, MAC: mac, IP: i.IP,
			Hostname: i.Hostname, LastSeen: i.LastSeen, Match: kind})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].LastSeen.After(out[b].LastSeen) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func looksLikeMAC(q string) (string, bool) {
	clean := strings.ToLower(strings.NewReplacer(":", "", "-", "", ".", "").Replace(q))
	if len(clean) < 4 || len(clean) > 12 {
		return "", false
	}
	for _, r := range clean {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return "", false
		}
	}
	return clean, true
}
