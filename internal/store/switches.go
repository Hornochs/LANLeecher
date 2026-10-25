package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
)

// Switch is a monitored switch.
type Switch struct {
	ID             int64
	Name           string
	Host           string
	Port           int
	SNMPVersion    string // v2c or v3
	CredentialsEnc []byte
	// PollInterval in seconds; 0 uses the config default.
	PollInterval int
	FDBAdapter   string
	ARPSource    bool
	Enabled      bool
	SysDescr     string
	SysName      string
	LastPollAt   *time.Time
	LastPollOK   bool
	LastError    string
	CreatedAt    time.Time
}

const switchCols = `id, name, host, port, snmp_version, credentials_enc, poll_interval, fdb_adapter,
	arp_source, enabled, sys_descr, sys_name, last_poll_at, last_poll_ok, last_error, created_at`

func scanSwitch(row interface{ Scan(...any) error }) (*Switch, error) {
	var s Switch
	var lastPoll sql.NullInt64
	var created int64
	err := row.Scan(&s.ID, &s.Name, &s.Host, &s.Port, &s.SNMPVersion, &s.CredentialsEnc, &s.PollInterval,
		&s.FDBAdapter, &s.ARPSource, &s.Enabled, &s.SysDescr, &s.SysName, &lastPoll, &s.LastPollOK,
		&s.LastError, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.LastPollAt = fromNullUnix(lastPoll)
	s.CreatedAt = fromUnix(created)
	return &s, nil
}

// ListSwitches returns all switches ordered by name.
func (db *DB) ListSwitches(ctx context.Context) ([]*Switch, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+switchCols+` FROM switches ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Switch
	for rows.Next() {
		s, err := scanSwitch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSwitch returns one switch.
func (db *DB) GetSwitch(ctx context.Context, id int64) (*Switch, error) {
	return scanSwitch(db.sql.QueryRowContext(ctx, `SELECT `+switchCols+` FROM switches WHERE id = ?`, id))
}

// CreateSwitch inserts s and sets s.ID.
func (db *DB) CreateSwitch(ctx context.Context, s *Switch) error {
	s.CreatedAt = Now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO switches
		(name, host, port, snmp_version, credentials_enc, poll_interval, fdb_adapter, arp_source, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.Host, s.Port, s.SNMPVersion, s.CredentialsEnc, s.PollInterval, s.FDBAdapter,
		boolInt(s.ARPSource), boolInt(s.Enabled), unix(s.CreatedAt))
	if err != nil {
		return err
	}
	s.ID, err = res.LastInsertId()
	return err
}

// UpdateSwitch saves the editable fields of s.
func (db *DB) UpdateSwitch(ctx context.Context, s *Switch) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE switches SET name = ?, host = ?, port = ?, snmp_version = ?,
		credentials_enc = ?, poll_interval = ?, fdb_adapter = ?, arp_source = ?, enabled = ? WHERE id = ?`,
		s.Name, s.Host, s.Port, s.SNMPVersion, s.CredentialsEnc, s.PollInterval, s.FDBAdapter,
		boolInt(s.ARPSource), boolInt(s.Enabled), s.ID)
	return err
}

// DeleteSwitch removes a switch and its ports. Traffic totals of past events
// that reference its ports are deleted with them.
func (db *DB) DeleteSwitch(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM switches WHERE id = ?`, id)
	return err
}

// SetSwitchStatus records the outcome of a poll.
func (db *DB) SetSwitchStatus(ctx context.Context, id int64, ok bool, errText string, at time.Time) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE switches SET last_poll_at = ?, last_poll_ok = ?, last_error = ? WHERE id = ?`,
		unix(at), boolInt(ok), errText, id)
	return err
}

// SetSwitchSystem stores sysDescr and sysName.
func (db *DB) SetSwitchSystem(ctx context.Context, id int64, descr, name string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE switches SET sys_descr = ?, sys_name = ? WHERE id = ?`, descr, name, id)
	return err
}

// Port is a switch interface.
type Port struct {
	ID            int64
	SwitchID      int64
	SwitchName    string
	IfIndex       int
	IfName        string
	IfAlias       string
	IfDescr       string
	IfType        int
	Physical      bool
	LLDPNeighbor  string
	OperUp        bool
	SpeedMbps     uint64
	HCSupported   bool
	Role          accounting.Role
	Label         string
	Hidden        bool
	MACCount      int
	UplinkSuspect bool
	// UplinkDismissed is set when an operator confirmed that the port is not
	// an uplink; the uplink hint then stays off.
	UplinkDismissed bool
	LastSeen        time.Time
}

// Name returns the label, or switch and interface name.
func (p *Port) Name() string {
	if p.Label != "" {
		return p.Label
	}
	return p.SwitchName + " " + p.ShortName()
}

// ShortName returns ifName, falling back to ifDescr.
func (p *Port) ShortName() string {
	if p.IfName != "" {
		return p.IfName
	}
	if p.IfDescr != "" {
		return p.IfDescr
	}
	return "if" + itoa(int64(p.IfIndex))
}

const portCols = `p.id, p.switch_id, s.name, p.if_index, p.if_name, p.if_alias, p.if_descr, p.if_type, p.physical,
	p.lldp_neighbor, p.oper_up, p.speed_mbps, p.hc_supported, p.role, p.label, p.hidden_in_rankings, p.mac_count,
	p.uplink_suspect, p.uplink_dismissed, p.last_seen`

func portDest(p *Port, role *string, seen *int64) []any {
	return []any{&p.ID, &p.SwitchID, &p.SwitchName, &p.IfIndex, &p.IfName, &p.IfAlias, &p.IfDescr, &p.IfType, &p.Physical,
		&p.LLDPNeighbor, &p.OperUp, &p.SpeedMbps, &p.HCSupported, role, &p.Label, &p.Hidden, &p.MACCount,
		&p.UplinkSuspect, &p.UplinkDismissed, seen}
}

func scanPort(row interface{ Scan(...any) error }) (*Port, error) {
	var p Port
	var seen int64
	var role string
	err := row.Scan(portDest(&p, &role, &seen)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Role = accounting.Role(role)
	p.LastSeen = fromUnix(seen)
	return &p, nil
}

// ListPorts returns the ports of a switch by ifIndex.
func (db *DB) ListPorts(ctx context.Context, switchID int64) ([]*Port, error) {
	return listPorts(ctx, db.sql, switchID)
}

func listPorts(ctx context.Context, q querier, switchID int64) ([]*Port, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+portCols+` FROM ports p JOIN switches s ON s.id = p.switch_id
		WHERE p.switch_id = ? ORDER BY p.if_index`, switchID)
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

// GetPort returns one port.
func (db *DB) GetPort(ctx context.Context, id int64) (*Port, error) {
	return scanPort(db.sql.QueryRowContext(ctx, `SELECT `+portCols+` FROM ports p JOIN switches s ON s.id = p.switch_id
		WHERE p.id = ?`, id))
}

// PortObservation is what a poll learned about an interface.
type PortObservation struct {
	IfIndex     int
	IfName      string
	IfAlias     string
	IfDescr     string
	IfType      int
	Physical    bool
	OperUp      bool
	SpeedMbps   uint64
	HCSupported bool
	// DefaultRole applies only when the port is created.
	DefaultRole accounting.Role
}

// UpsertPorts creates or updates the observed ports of a switch and returns
// all of its ports keyed by ifIndex. Role and visibility are set only when a
// port is created; afterwards they belong to the admin.
func (tx *Tx) UpsertPorts(ctx context.Context, switchID int64, obs []PortObservation, at time.Time) (map[int]*Port, error) {
	stmt, err := tx.tx.PrepareContext(ctx, `INSERT INTO ports
		(switch_id, if_index, if_name, if_alias, if_descr, if_type, physical, oper_up, speed_mbps, hc_supported,
		 role, hidden_in_rankings, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (switch_id, if_index) DO UPDATE SET
			if_name = excluded.if_name, if_alias = excluded.if_alias, if_descr = excluded.if_descr,
			if_type = excluded.if_type, physical = excluded.physical,
			oper_up = excluded.oper_up, speed_mbps = excluded.speed_mbps,
			hc_supported = excluded.hc_supported, last_seen = excluded.last_seen`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	for _, o := range obs {
		role := o.DefaultRole
		if !role.Valid() {
			role = accounting.RoleClient
		}
		if _, err := stmt.ExecContext(ctx, switchID, o.IfIndex, o.IfName, o.IfAlias, o.IfDescr, o.IfType,
			boolInt(o.Physical), boolInt(o.OperUp), int64(o.SpeedMbps), boolInt(o.HCSupported), string(role),
			boolInt(role.HiddenByDefault()), unix(at)); err != nil {
			return nil, err
		}
	}
	ports, err := listPorts(ctx, tx.tx, switchID)
	if err != nil {
		return nil, err
	}
	out := make(map[int]*Port, len(ports))
	for _, p := range ports {
		out[p.IfIndex] = p
	}
	return out, nil
}

// PortSettings are the admin-editable port fields. Nil fields stay unchanged.
type PortSettings struct {
	Role   *accounting.Role
	Label  *string
	Hidden *bool
	// UplinkDismissed confirms (true) that a port is not an uplink, or
	// withdraws that confirmation (false).
	UplinkDismissed *bool
	// UplinkThreshold is polling.uplink_mac_threshold. It is used to show the
	// hint again right away when a confirmation is withdrawn.
	UplinkThreshold int
}

// UpdatePorts applies settings to several ports. Setting a role without an
// explicit Hidden value applies the role's default visibility. Making a port
// an uplink clears a "not an uplink" confirmation.
func (db *DB) UpdatePorts(ctx context.Context, ids []int64, s PortSettings) error {
	return db.InTx(ctx, func(tx *Tx) error {
		for _, id := range ids {
			if s.Role != nil {
				hidden := s.Role.HiddenByDefault()
				if s.Hidden != nil {
					hidden = *s.Hidden
				}
				if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET role = ?, hidden_in_rankings = ? WHERE id = ?`,
					string(*s.Role), boolInt(hidden), id); err != nil {
					return err
				}
			} else if s.Hidden != nil {
				if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET hidden_in_rankings = ? WHERE id = ?`,
					boolInt(*s.Hidden), id); err != nil {
					return err
				}
			}
			if s.Label != nil {
				if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET label = ? WHERE id = ?`, *s.Label, id); err != nil {
					return err
				}
			}
			switch {
			case s.UplinkDismissed != nil && *s.UplinkDismissed:
				if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET uplink_dismissed = 1, uplink_suspect = 0
					WHERE id = ? AND role <> 'uplink'`, id); err != nil {
					return err
				}
			case s.UplinkDismissed != nil:
				// Re-evaluate the hint from the last forwarding-table read
				// instead of waiting for the next one.
				if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET uplink_dismissed = 0,
					uplink_suspect = CASE WHEN role <> 'uplink' AND (lldp_neighbor <> '' OR mac_count > ?) THEN 1 ELSE 0 END
					WHERE id = ?`, s.UplinkThreshold, id); err != nil {
					return err
				}
			}
			if _, err := tx.tx.ExecContext(ctx, `UPDATE ports SET uplink_dismissed = 0 WHERE id = ? AND role = 'uplink'`,
				id); err != nil {
				return err
			}
		}
		return nil
	})
}

// CounterState is the last raw counter reading of a port.
type CounterState struct {
	PortID        int64
	In, Out       uint64
	SysUptime     uint32
	Discontinuity uint32
	PolledAt      time.Time
}

// LoadCounterStates returns the counter state of all ports of a switch.
func (db *DB) LoadCounterStates(ctx context.Context, switchID int64) (map[int64]CounterState, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT c.port_id, c.in_raw, c.out_raw, c.sys_uptime, c.discontinuity, c.polled_at
		FROM port_counter_state c JOIN ports p ON p.id = c.port_id WHERE p.switch_id = ?`, switchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]CounterState{}
	for rows.Next() {
		var c CounterState
		var in, outRaw, polled int64
		var up, disc int64
		if err := rows.Scan(&c.PortID, &in, &outRaw, &up, &disc, &polled); err != nil {
			return nil, err
		}
		// uint64 counters are stored as int64 with the same bit pattern.
		c.In, c.Out = uint64(in), uint64(outRaw)
		c.SysUptime, c.Discontinuity = uint32(up), uint32(disc)
		c.PolledAt = fromUnix(polled)
		out[c.PortID] = c
	}
	return out, rows.Err()
}

// SaveCounterState stores the latest raw reading of a port.
func (tx *Tx) SaveCounterState(ctx context.Context, c CounterState) error {
	_, err := tx.tx.ExecContext(ctx, `INSERT INTO port_counter_state
		(port_id, in_raw, out_raw, counter_bits, sys_uptime, discontinuity, polled_at) VALUES (?, ?, ?, 64, ?, ?, ?)
		ON CONFLICT (port_id) DO UPDATE SET in_raw = excluded.in_raw, out_raw = excluded.out_raw,
			sys_uptime = excluded.sys_uptime, discontinuity = excluded.discontinuity, polled_at = excluded.polled_at`,
		c.PortID, int64(c.In), int64(c.Out), int64(c.SysUptime), int64(c.Discontinuity), unix(c.PolledAt))
	return err
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
