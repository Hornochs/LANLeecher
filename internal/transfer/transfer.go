// Package transfer defines the export file format that moves recorded data
// (switches, ports, devices, MACs, events and their traffic) from one
// LANLeecher instance to another, and reads and writes it safely.
//
// The file is gzip-compressed JSON. It never contains credentials, users,
// sessions, API tokens, the audit log or DHCP sources. All references use
// natural keys (switch name, ifIndex, MAC, export-local group keys), so the
// importing instance can map them onto its own surrogate IDs.
package transfer

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

// Format identifies an export file.
const Format = "lanleecher-export"

// Version is the format version this build writes and reads.
const Version = 1

// Limits for reading an export.
const (
	// MaxCompressed is the largest file accepted (upload limit).
	MaxCompressed = 128 << 20
	// MaxDecompressed guards against compression bombs.
	MaxDecompressed = 1 << 30
	maxText         = 4096
	maxName         = 255
)

// Subject types of traffic rows (same values as in the database).
const (
	SubjectMAC        = "mac"
	SubjectPortGroup  = "port_group"
	SubjectUnassigned = "unassigned"
)

// Doc is a complete export.
type Doc struct {
	Format     string   `json:"format"`
	Version    int      `json:"version"`
	ExportedAt int64    `json:"exported_at"`
	Software   string   `json:"software"`
	Source     string   `json:"source,omitempty"`
	Switches   []Switch `json:"switches"`
	Devices    []Device `json:"devices"`
	MACs       []MAC    `json:"macs"`
	Events     []Event  `json:"events"`
}

// Switch is a switch without credentials. Name is unique within a Doc and
// is the key PortRef uses.
type Switch struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	SysName    string `json:"sys_name,omitempty"`
	SysDescr   string `json:"sys_descr,omitempty"`
	FDBAdapter string `json:"fdb_adapter,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	Ports      []Port `json:"ports"`
}

// Port is a switch port.
type Port struct {
	IfIndex         int    `json:"if_index"`
	IfName          string `json:"if_name,omitempty"`
	IfAlias         string `json:"if_alias,omitempty"`
	IfDescr         string `json:"if_descr,omitempty"`
	IfType          int    `json:"if_type,omitempty"`
	Physical        bool   `json:"physical"`
	SpeedMbps       uint64 `json:"speed_mbps,omitempty"`
	Role            string `json:"role"`
	Label           string `json:"label,omitempty"`
	Hidden          bool   `json:"hidden,omitempty"`
	UplinkDismissed bool   `json:"uplink_dismissed,omitempty"`
	LastSeen        int64  `json:"last_seen"`
}

// PortRef points at a port by switch name and ifIndex.
type PortRef struct {
	Switch  string `json:"switch"`
	IfIndex int    `json:"if_index"`
}

// Device groups MACs. Key is unique within a Doc.
type Device struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name,omitempty"`
	Hidden      bool   `json:"hidden,omitempty"`
	Notes       string `json:"notes,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

// MAC is one MAC address with its history.
type MAC struct {
	MAC       string     `json:"mac"`
	Device    string     `json:"device"`
	Vendor    string     `json:"vendor,omitempty"`
	FirstSeen int64      `json:"first_seen"`
	LastSeen  int64      `json:"last_seen"`
	Locations []Location `json:"locations,omitempty"`
	IPs       []History  `json:"ips,omitempty"`
	Hostnames []History  `json:"hostnames,omitempty"`
}

// Location is a port a MAC was seen on.
type Location struct {
	Port      PortRef `json:"port"`
	VLAN      int     `json:"vlan,omitempty"`
	FirstSeen int64   `json:"first_seen"`
	LastSeen  int64   `json:"last_seen"`
}

// History is an IP address or hostname of a MAC from one source.
type History struct {
	Value     string `json:"value"`
	Source    string `json:"source"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
}

// Event is an event with all its traffic.
type Event struct {
	Slug       string      `json:"slug"`
	Name       string      `json:"name"`
	Notes      string      `json:"notes,omitempty"`
	StartedAt  int64       `json:"started_at"`
	EndedAt    *int64      `json:"ended_at"`
	PortTotals []PortTotal `json:"port_totals,omitempty"`
	PortGroups []PortGroup `json:"port_groups,omitempty"`
	Totals     []Traffic   `json:"totals,omitempty"`
	Buckets    []Traffic   `json:"buckets,omitempty"`
}

// PortTotal is the traffic booked on a port in an event (every role).
type PortTotal struct {
	Port      PortRef `json:"port"`
	Down      int64   `json:"down"`
	Up        int64   `json:"up"`
	UpdatedAt int64   `json:"updated_at"`
}

// PortGroup is the shared ranking entry of a port with several MACs. Key is
// unique within its event.
type PortGroup struct {
	Key  string     `json:"key"`
	Port PortRef    `json:"port"`
	MACs []GroupMAC `json:"macs,omitempty"`
}

// GroupMAC is a member of a port group.
type GroupMAC struct {
	MAC       string `json:"mac"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
}

// Traffic is a row of traffic_totals (Start = 0) or traffic_buckets.
// Subject is the MAC for "mac", the group key for "port_group" and empty
// for "unassigned" (the port is the subject then).
type Traffic struct {
	Type      string  `json:"type"`
	Subject   string  `json:"subject,omitempty"`
	Port      PortRef `json:"port"`
	Start     int64   `json:"start,omitempty"`
	Down      int64   `json:"down"`
	Up        int64   `json:"up"`
	UpdatedAt int64   `json:"updated_at,omitempty"`
}

// Write encodes d as gzip-compressed JSON.
func Write(w io.Writer, d *Doc) error {
	gz := gzip.NewWriter(w)
	enc := json.NewEncoder(gz)
	if err := enc.Encode(d); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

// ErrTooLarge is returned when a file exceeds MaxCompressed or MaxDecompressed.
var ErrTooLarge = errors.New("export file too large")

// Read decodes and validates an export. Plain (uncompressed) JSON is
// accepted as well. The reader is limited to MaxCompressed bytes, the
// decompressed data to MaxDecompressed bytes.
func Read(r io.Reader) (*Doc, error) {
	lim := &limitReader{r: r, n: MaxCompressed}
	br := bufio.NewReader(lim)
	var src io.Reader = br
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("read export: %w", err)
		}
		defer gz.Close()
		src = &limitReader{r: gz, n: MaxDecompressed}
	}
	var d Doc
	dec := json.NewDecoder(src)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		if errors.Is(err, ErrTooLarge) {
			return nil, err
		}
		return nil, fmt.Errorf("read export: %w", err)
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// limitReader fails with ErrTooLarge instead of silently truncating.
type limitReader struct {
	r io.Reader
	n int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		// Allow a clean EOF exactly at the limit.
		var one [1]byte
		if n, err := l.r.Read(one[:]); n == 0 && err != nil {
			return 0, err
		}
		return 0, ErrTooLarge
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}

var slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidationError describes why a Doc is rejected.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "invalid export: " + e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

func text(s string, max int) bool { return len(s) <= max && utf8.ValidString(s) }

// Validate checks the format header, limits and that every reference
// resolves. MACs are normalised in place.
func (d *Doc) Validate() error {
	if d.Format != Format {
		return invalid("not a LANLeecher export (format %q)", d.Format)
	}
	if d.Version < 1 || d.Version > Version {
		return invalid("unsupported version %d (this build reads up to %d)", d.Version, Version)
	}
	if !text(d.Software, maxName) || !text(d.Source, maxName) {
		return invalid("header text too long")
	}
	ports := map[PortRef]bool{}
	switches := map[string]bool{}
	for i := range d.Switches {
		s := &d.Switches[i]
		if s.Name == "" || !text(s.Name, maxName) || !text(s.Host, maxName) || !text(s.SysName, maxName) ||
			!text(s.SysDescr, maxText) || !text(s.FDBAdapter, 32) {
			return invalid("switch %d: bad name or text field", i)
		}
		if switches[s.Name] {
			return invalid("switch %q listed twice", s.Name)
		}
		switches[s.Name] = true
		if s.Port < 0 || s.Port > 65535 {
			return invalid("switch %q: bad port %d", s.Name, s.Port)
		}
		for j := range s.Ports {
			p := &s.Ports[j]
			ref := PortRef{Switch: s.Name, IfIndex: p.IfIndex}
			if p.IfIndex <= 0 || ports[ref] {
				return invalid("switch %q: bad or duplicate ifIndex %d", s.Name, p.IfIndex)
			}
			ports[ref] = true
			if !accounting.Role(p.Role).Valid() {
				return invalid("switch %q port %d: unknown role %q", s.Name, p.IfIndex, p.Role)
			}
			if !text(p.IfName, maxName) || !text(p.IfAlias, maxName) || !text(p.IfDescr, maxName) || !text(p.Label, maxName) {
				return invalid("switch %q port %d: text field too long", s.Name, p.IfIndex)
			}
		}
	}
	checkPort := func(ctx string, r PortRef) error {
		if !ports[r] {
			return invalid("%s: unknown port %s/%d", ctx, r.Switch, r.IfIndex)
		}
		return nil
	}
	devices := map[string]bool{}
	for i, dv := range d.Devices {
		if dv.Key == "" || !text(dv.Key, 64) || devices[dv.Key] {
			return invalid("device %d: missing or duplicate key", i)
		}
		if !text(dv.DisplayName, maxName) || !text(dv.Notes, maxText) {
			return invalid("device %q: text field too long", dv.Key)
		}
		devices[dv.Key] = true
	}
	macs := map[string]bool{}
	for i := range d.MACs {
		m := &d.MACs[i]
		norm, err := unicastMAC(m.MAC)
		if err != nil {
			return invalid("mac %d: %v", i, err)
		}
		m.MAC = norm
		if macs[norm] {
			return invalid("mac %s listed twice", norm)
		}
		macs[norm] = true
		if !devices[m.Device] {
			return invalid("mac %s: unknown device %q", norm, m.Device)
		}
		if !text(m.Vendor, maxName) || m.LastSeen < m.FirstSeen {
			return invalid("mac %s: bad vendor or times", norm)
		}
		for _, l := range m.Locations {
			if err := checkPort("mac "+norm, l.Port); err != nil {
				return err
			}
		}
		for _, h := range append(append([]History{}, m.IPs...), m.Hostnames...) {
			if h.Value == "" || !text(h.Value, maxName) || h.Source == "" || !text(h.Source, 32) {
				return invalid("mac %s: bad history entry", norm)
			}
		}
	}
	slugs := map[string]bool{}
	for i := range d.Events {
		e := &d.Events[i]
		if !slugRE.MatchString(e.Slug) || len(e.Slug) > 80 || slugs[e.Slug] {
			return invalid("event %d: bad or duplicate slug %q", i, e.Slug)
		}
		slugs[e.Slug] = true
		if e.Name == "" || !text(e.Name, maxName) || !text(e.Notes, maxText) {
			return invalid("event %q: bad name or notes", e.Slug)
		}
		if e.EndedAt != nil && *e.EndedAt < e.StartedAt {
			return invalid("event %q: ends before it starts", e.Slug)
		}
		for _, t := range e.PortTotals {
			if err := checkPort("event "+e.Slug, t.Port); err != nil {
				return err
			}
			if t.Down < 0 || t.Up < 0 {
				return invalid("event %q: negative traffic", e.Slug)
			}
		}
		groups := map[string]bool{}
		for gi := range e.PortGroups {
			g := &e.PortGroups[gi]
			if g.Key == "" || !text(g.Key, 64) || groups[g.Key] {
				return invalid("event %q: missing or duplicate port group key", e.Slug)
			}
			groups[g.Key] = true
			if err := checkPort("event "+e.Slug, g.Port); err != nil {
				return err
			}
			for mi := range g.MACs {
				norm, err := unicastMAC(g.MACs[mi].MAC)
				if err != nil {
					return invalid("event %q group %q: %v", e.Slug, g.Key, err)
				}
				g.MACs[mi].MAC = norm
			}
		}
		for _, rows := range [][]Traffic{e.Totals, e.Buckets} {
			for ti := range rows {
				t := &rows[ti]
				if err := checkPort("event "+e.Slug, t.Port); err != nil {
					return err
				}
				if t.Down < 0 || t.Up < 0 || t.Start < 0 {
					return invalid("event %q: negative traffic or time", e.Slug)
				}
				switch t.Type {
				case SubjectMAC:
					norm, err := unicastMAC(t.Subject)
					if err != nil {
						return invalid("event %q: %v", e.Slug, err)
					}
					if !macs[norm] {
						return invalid("event %q: traffic for unlisted MAC %s", e.Slug, norm)
					}
					t.Subject = norm
				case SubjectPortGroup:
					if !groups[t.Subject] {
						return invalid("event %q: unknown port group %q", e.Slug, t.Subject)
					}
				case SubjectUnassigned:
					if t.Subject != "" {
						return invalid("event %q: unassigned traffic with a subject", e.Slug)
					}
				default:
					return invalid("event %q: unknown subject type %q", e.Slug, t.Type)
				}
			}
		}
	}
	return nil
}

func unicastMAC(s string) (string, error) {
	m, err := macaddr.Parse(s)
	if err != nil {
		return "", fmt.Errorf("bad MAC %q", s)
	}
	if m.IsZero() || m.IsMulticast() {
		return "", fmt.Errorf("MAC %s is not a unicast address", m)
	}
	return m.String(), nil
}
