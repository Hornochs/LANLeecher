package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
)

// WANLink is a named internet uplink. Ports with the role "wan" belong to
// one; a party can have several.
type WANLink struct {
	ID   int64
	Name string
	Sort int
	// DownMbps and UpMbps are the booked bandwidth (0 = unknown), used for
	// the utilisation display.
	DownMbps  int64
	UpMbps    int64
	Notes     string
	CreatedAt time.Time
	// Ports are the WAN ports of this link (ListWANLinks only).
	Ports []*Port
}

// Errors of WAN link management.
var (
	ErrWANLinkInUse   = errors.New("WAN link still has ports")
	ErrWANLinkInvalid = errors.New("WAN link name must be 1 to 80 characters, bandwidth not negative")
	ErrWANLinkExists  = errors.New("a WAN link with this name exists")
)

func (l *WANLink) normalise() error {
	l.Name = strings.TrimSpace(l.Name)
	l.Notes = strings.TrimSpace(l.Notes)
	if l.Name == "" || utf8.RuneCountInString(l.Name) > 80 || !utf8.ValidString(l.Name) ||
		l.DownMbps < 0 || l.UpMbps < 0 || len(l.Notes) > 4096 {
		return ErrWANLinkInvalid
	}
	return nil
}

const wanCols = `id, name, sort, down_mbps, up_mbps, notes, created_at`

func scanWANLink(row interface{ Scan(...any) error }) (*WANLink, error) {
	var l WANLink
	var created int64
	err := row.Scan(&l.ID, &l.Name, &l.Sort, &l.DownMbps, &l.UpMbps, &l.Notes, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.CreatedAt = fromUnix(created)
	return &l, nil
}

// ListWANLinks returns all WAN links ordered by sort and name, each with its
// WAN ports.
func (db *DB) ListWANLinks(ctx context.Context) ([]*WANLink, error) {
	var links []*WANLink
	byID := map[int64]*WANLink{}
	if err := queryEach(ctx, db.sql, `SELECT `+wanCols+` FROM wan_links ORDER BY sort, name`, nil, func(r *sql.Rows) error {
		l, err := scanWANLink(r)
		if err != nil {
			return err
		}
		links = append(links, l)
		byID[l.ID] = l
		return nil
	}); err != nil {
		return nil, err
	}
	rows, err := db.sql.QueryContext(ctx, `SELECT `+portCols+` FROM ports p JOIN switches s ON s.id = p.switch_id
		WHERE p.role = 'wan' AND p.wan_link_id IS NOT NULL ORDER BY s.name, p.if_index`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanPort(rows)
		if err != nil {
			return nil, err
		}
		if l := byID[p.WANLinkID]; l != nil {
			l.Ports = append(l.Ports, p)
		}
	}
	return links, rows.Err()
}

// GetWANLink returns one WAN link (without ports).
func (db *DB) GetWANLink(ctx context.Context, id int64) (*WANLink, error) {
	return scanWANLink(db.sql.QueryRowContext(ctx, `SELECT `+wanCols+` FROM wan_links WHERE id = ?`, id))
}

// CreateWANLink inserts l and sets its ID.
func (db *DB) CreateWANLink(ctx context.Context, l *WANLink) error {
	if err := l.normalise(); err != nil {
		return err
	}
	l.CreatedAt = Now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO wan_links (name, sort, down_mbps, up_mbps, notes, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, l.Name, l.Sort, l.DownMbps, l.UpMbps, l.Notes, unix(l.CreatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrWANLinkExists
		}
		return err
	}
	l.ID, err = res.LastInsertId()
	return err
}

// UpdateWANLink saves name, sort, bandwidth and notes.
func (db *DB) UpdateWANLink(ctx context.Context, l *WANLink) error {
	if err := l.normalise(); err != nil {
		return err
	}
	res, err := db.sql.ExecContext(ctx, `UPDATE wan_links SET name = ?, sort = ?, down_mbps = ?, up_mbps = ?, notes = ?
		WHERE id = ?`, l.Name, l.Sort, l.DownMbps, l.UpMbps, l.Notes, l.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrWANLinkExists
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWANLink removes a WAN link that has no ports any more.
func (db *DB) DeleteWANLink(ctx context.Context, id int64) error {
	return db.InTx(ctx, func(tx *Tx) error {
		var n int
		if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ports WHERE wan_link_id = ? AND role = 'wan'`, id).
			Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrWANLinkInUse
		}
		res, err := tx.tx.ExecContext(ctx, `DELETE FROM wan_links WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// WANPortTraffic is the traffic of one WAN port, from the internet's point
// of view (InternetDown = downloaded by the LAN).
type WANPortTraffic struct {
	Port         *Port
	InternetDown int64
	InternetUp   int64
}

// WANLinkTraffic is the traffic of one WAN link in an event.
type WANLinkTraffic struct {
	Link         *WANLink
	InternetDown int64
	InternetUp   int64
	Ports        []WANPortTraffic
	// Series has 5-minute buckets in internet view (BytesDown = download),
	// only when requested.
	Series []Bucket
}

// WANTraffic returns the traffic of every WAN link in an event (links
// without ports or traffic included, with zeros). Ports count by their
// current role and link: port traffic is booked for every role, so a WAN
// port configured after the event still reports its traffic.
func (db *DB) WANTraffic(ctx context.Context, eventID int64, series bool) ([]WANLinkTraffic, error) {
	links, err := db.ListWANLinks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WANLinkTraffic, len(links))
	idx := map[int64]int{}
	for i, l := range links {
		out[i].Link = l
		idx[l.ID] = i
	}
	totals := map[int64][2]int64{}
	if err := queryEach(ctx, db.sql, `SELECT t.port_id, t.bytes_down, t.bytes_up FROM port_traffic_totals t
		JOIN ports p ON p.id = t.port_id WHERE t.event_id = ? AND p.role = 'wan' AND p.wan_link_id IS NOT NULL`,
		[]any{eventID}, func(r *sql.Rows) error {
			var id, down, up int64
			if err := r.Scan(&id, &down, &up); err != nil {
				return err
			}
			totals[id] = [2]int64{down, up}
			return nil
		}); err != nil {
		return nil, err
	}
	for i, l := range links {
		for _, p := range l.Ports {
			t := totals[p.ID]
			down, up := accounting.WANDirection(t[0], t[1])
			out[i].Ports = append(out[i].Ports, WANPortTraffic{Port: p, InternetDown: down, InternetUp: up})
			out[i].InternetDown += down
			out[i].InternetUp += up
		}
	}
	if !series {
		return out, nil
	}
	err = queryEach(ctx, db.sql, `SELECT p.wan_link_id, b.bucket_start, SUM(b.bytes_down), SUM(b.bytes_up)
		FROM port_traffic_buckets b JOIN ports p ON p.id = b.port_id
		WHERE b.event_id = ? AND p.role = 'wan' AND p.wan_link_id IS NOT NULL
		GROUP BY p.wan_link_id, b.bucket_start ORDER BY p.wan_link_id, b.bucket_start`, []any{eventID},
		func(r *sql.Rows) error {
			var link, start, down, up int64
			if err := r.Scan(&link, &start, &down, &up); err != nil {
				return err
			}
			i, ok := idx[link]
			if !ok {
				return nil
			}
			d, u := accounting.WANDirection(down, up)
			out[i].Series = append(out[i].Series, Bucket{Start: fromUnix(start), BytesDown: d, BytesUp: u})
			return nil
		})
	return out, err
}
