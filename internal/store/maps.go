package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrExists is returned when a map with the name exists.
var ErrExists = errors.New("already exists")

func isUnique(err error) bool { return err != nil && strings.Contains(err.Error(), "UNIQUE") }

// Map link kinds.
const (
	MapLinkManual = "manual" // drawn by an admin
	MapLinkHidden = "hidden" // an automatic link an admin hid
)

// Map is a network map: switches placed on a background image.
type Map struct {
	ID       int64
	Name     string
	Sort     int
	Refresh  int // seconds between updates in the browser (5–300)
	BGType   string
	BGWidth  int
	BGHeight int
	BGHash   string // empty: no background image
	Created  time.Time
	Updated  time.Time
}

// HasBackground reports whether a background image is stored.
func (m *Map) HasBackground() bool { return m.BGHash != "" }

// MapNode is a switch on a map; X and Y are fractions (0–1) of the map size.
type MapNode struct {
	SwitchID int64
	X, Y     float64
}

// MapWANNode is an internet connection (WAN link) on a map.
type MapWANNode struct {
	WANLinkID int64
	X, Y      float64
}

// MapLink is a link drawn by hand or a hidden automatic link.
type MapLink struct {
	ID      int64
	Kind    string
	ASwitch int64
	AIf     int
	BSwitch int64
	BIf     int
}

// Same reports whether l connects the same two switch ports as the given
// ends, in either direction (ifIndex 0 matches any port).
func (l *MapLink) Same(aDev int64, aIf int, bDev int64, bIf int) bool {
	match := func(d1 int64, i1 int, d2 int64, i2 int) bool {
		return d1 == d2 && (i1 == 0 || i2 == 0 || i1 == i2)
	}
	return (match(l.ASwitch, l.AIf, aDev, aIf) && match(l.BSwitch, l.BIf, bDev, bIf)) ||
		(match(l.ASwitch, l.AIf, bDev, bIf) && match(l.BSwitch, l.BIf, aDev, aIf))
}

const mapCols = `id, name, sort, refresh, bg_type, bg_width, bg_height, bg_hash, created_at, updated_at`

func scanMap(row interface{ Scan(...any) error }) (*Map, error) {
	var m Map
	var created, updated int64
	err := row.Scan(&m.ID, &m.Name, &m.Sort, &m.Refresh, &m.BGType, &m.BGWidth, &m.BGHeight, &m.BGHash, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.Created, m.Updated = fromUnix(created), fromUnix(updated)
	return &m, nil
}

func validMap(m *Map) error {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || len(m.Name) > 80 {
		return errors.New("map name must have 1 to 80 characters")
	}
	if m.Refresh == 0 {
		m.Refresh = 15
	}
	if m.Refresh < 5 || m.Refresh > 300 {
		return errors.New("map refresh must be between 5 and 300 seconds")
	}
	return nil
}

// ListMaps returns all maps (without background data).
func (db *DB) ListMaps(ctx context.Context) ([]*Map, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+mapCols+` FROM maps ORDER BY sort, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Map
	for rows.Next() {
		m, err := scanMap(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MapByID returns a map (without background data).
func (db *DB) MapByID(ctx context.Context, id int64) (*Map, error) {
	return scanMap(db.sql.QueryRowContext(ctx, `SELECT `+mapCols+` FROM maps WHERE id = ?`, id))
}

// CreateMap stores a new map.
func (db *DB) CreateMap(ctx context.Context, m *Map) error {
	if err := validMap(m); err != nil {
		return err
	}
	now := Now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO maps (name, sort, refresh, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		m.Name, m.Sort, m.Refresh, unix(now), unix(now))
	if isUnique(err) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	m.ID, err = res.LastInsertId()
	m.Created, m.Updated = now, now
	return err
}

// UpdateMap saves name, sort and refresh.
func (db *DB) UpdateMap(ctx context.Context, m *Map) error {
	if err := validMap(m); err != nil {
		return err
	}
	res, err := db.sql.ExecContext(ctx, `UPDATE maps SET name = ?, sort = ?, refresh = ?, updated_at = ? WHERE id = ?`,
		m.Name, m.Sort, m.Refresh, unix(Now()), m.ID)
	if isUnique(err) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMap removes a map with its nodes and links.
func (db *DB) DeleteMap(ctx context.Context, id int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM maps WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetMapBackground stores (or with nil data removes) the background image.
// The caller has checked type and size.
func (db *DB) SetMapBackground(ctx context.Context, id int64, data []byte, mediaType string, width, height int) error {
	hash := ""
	if data != nil {
		sum := sha256.Sum256(data)
		hash = hex.EncodeToString(sum[:8])
	} else {
		mediaType, width, height = "", 0, 0
	}
	res, err := db.sql.ExecContext(ctx, `UPDATE maps SET bg = ?, bg_type = ?, bg_width = ?, bg_height = ?, bg_hash = ?, updated_at = ? WHERE id = ?`,
		data, mediaType, width, height, hash, unix(Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MapBackground returns the background image, its media type and hash.
func (db *DB) MapBackground(ctx context.Context, id int64) ([]byte, string, string, error) {
	var data []byte
	var mediaType, hash string
	err := db.sql.QueryRowContext(ctx, `SELECT bg, bg_type, bg_hash FROM maps WHERE id = ?`, id).Scan(&data, &mediaType, &hash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && data == nil) {
		return nil, "", "", ErrNotFound
	}
	return data, mediaType, hash, err
}

// MapNodes returns the switches placed on a map.
func (db *DB) MapNodes(ctx context.Context, mapID int64) ([]MapNode, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT switch_id, x, y FROM map_nodes WHERE map_id = ? ORDER BY switch_id`, mapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MapNode
	for rows.Next() {
		var n MapNode
		if err := rows.Scan(&n.SwitchID, &n.X, &n.Y); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MapWANNodes returns the internet connections placed on a map.
func (db *DB) MapWANNodes(ctx context.Context, mapID int64) ([]MapWANNode, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT wan_link_id, x, y FROM map_wan_nodes WHERE map_id = ? ORDER BY wan_link_id`, mapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MapWANNode
	for rows.Next() {
		var n MapWANNode
		if err := rows.Scan(&n.WANLinkID, &n.X, &n.Y); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SetMapNodes replaces the switches and internet connections placed on a
// map. Coordinates are clamped to 0–1; unknown switches or WAN links fail
// the whole update.
func (db *DB) SetMapNodes(ctx context.Context, mapID int64, nodes []MapNode, wan []MapWANNode) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM map_nodes WHERE map_id = ?`, mapID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM map_wan_nodes WHERE map_id = ?`, mapID); err != nil {
		return err
	}
	clamp := func(v float64) float64 { return min(max(v, 0), 1) }
	for _, n := range nodes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO map_nodes (map_id, switch_id, x, y) VALUES (?, ?, ?, ?)`,
			mapID, n.SwitchID, clamp(n.X), clamp(n.Y)); err != nil {
			return fmt.Errorf("switch %d: %w", n.SwitchID, err)
		}
	}
	for _, n := range wan {
		if _, err := tx.ExecContext(ctx, `INSERT INTO map_wan_nodes (map_id, wan_link_id, x, y) VALUES (?, ?, ?, ?)`,
			mapID, n.WANLinkID, clamp(n.X), clamp(n.Y)); err != nil {
			return fmt.Errorf("WAN link %d: %w", n.WANLinkID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE maps SET updated_at = ? WHERE id = ?`, unix(Now()), mapID); err != nil {
		return err
	}
	return tx.Commit()
}

// MapLinks returns the manual and hidden links of a map.
func (db *DB) MapLinks(ctx context.Context, mapID int64) ([]MapLink, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, kind, a_switch, a_if, b_switch, b_if FROM map_links WHERE map_id = ? ORDER BY id`, mapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MapLink
	for rows.Next() {
		var l MapLink
		if err := rows.Scan(&l.ID, &l.Kind, &l.ASwitch, &l.AIf, &l.BSwitch, &l.BIf); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// AddMapLink stores a manual or hidden link.
func (db *DB) AddMapLink(ctx context.Context, mapID int64, l *MapLink) error {
	if l.Kind != MapLinkManual && l.Kind != MapLinkHidden {
		return fmt.Errorf("map link kind %q", l.Kind)
	}
	if l.ASwitch == l.BSwitch {
		return errors.New("a link needs two different switches")
	}
	res, err := db.sql.ExecContext(ctx, `INSERT INTO map_links (map_id, kind, a_switch, a_if, b_switch, b_if) VALUES (?, ?, ?, ?, ?, ?)`,
		mapID, l.Kind, l.ASwitch, l.AIf, l.BSwitch, l.BIf)
	if err != nil {
		return err
	}
	l.ID, err = res.LastInsertId()
	return err
}

// DeleteMapLink removes a manual or hidden link of a map.
func (db *DB) DeleteMapLink(ctx context.Context, mapID, linkID int64) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM map_links WHERE id = ? AND map_id = ?`, linkID, mapID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
