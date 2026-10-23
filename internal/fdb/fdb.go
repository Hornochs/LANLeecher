// Package fdb reads MAC-to-port assignments (forwarding database) and LLDP
// neighbors from switches.
package fdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosnmp/gosnmp"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/snmp"
)

// Entry is a MAC learned on an interface.
type Entry struct {
	MAC     macaddr.MAC
	IfIndex int
	VLAN    int
}

// Session gives a Source access to the switch, including per-VLAN views.
type Session interface {
	// Client returns the default connection.
	Client() *gosnmp.GoSNMP
	// VLAN returns a connection for a per-VLAN view (Cisco community@vlan).
	VLAN(ctx context.Context, vlan int) (*gosnmp.GoSNMP, error)
}

// Source reads the forwarding database. Implementations handle vendor quirks.
type Source interface {
	Name() string
	Read(ctx context.Context, s Session) ([]Entry, error)
}

// Adapters lists the available sources for the UI.
var Adapters = []string{"auto", "qbridge", "bridge", "cisco-vlan"}

// ForAdapter returns the source for a switch's fdb_adapter setting.
func ForAdapter(name string) (Source, error) {
	switch name {
	case "", "auto":
		return Auto{}, nil
	case "qbridge":
		return QBridge{}, nil
	case "bridge":
		return Bridge{}, nil
	case "cisco-vlan":
		return CiscoVLAN{}, nil
	}
	return nil, fmt.Errorf("unknown FDB adapter %q", name)
}

const (
	oidBasePortIfIndex = "1.3.6.1.2.1.17.1.4.1.2"
	oidDot1dFdbPort    = "1.3.6.1.2.1.17.4.3.1.2"
	oidDot1dFdbStatus  = "1.3.6.1.2.1.17.4.3.1.3"
	oidDot1qFdbPort    = "1.3.6.1.2.1.17.7.1.2.2.1.2"
	oidDot1qFdbStatus  = "1.3.6.1.2.1.17.7.1.2.2.1.3"
	oidVtpVlanState    = "1.3.6.1.4.1.9.9.46.1.3.1.1.2"
)

// FDB entry status (dot1dTpFdbStatus / dot1qTpFdbStatus).
const (
	statusInvalid = 2
	statusSelf    = 4
	statusMgmt    = 5
)

// Auto tries Q-BRIDGE-MIB first and falls back to BRIDGE-MIB when Q-BRIDGE
// delivers nothing or bridge ports that dot1dBasePortIfIndex does not know.
// Some firmware (e.g. TP-Link JetStream) reports port numbers (1, 2, …) in
// dot1qTpFdbPort but ifIndex values in dot1dBasePortIfIndex and
// dot1dTpFdbPort; only BRIDGE-MIB can be mapped there.
type Auto struct{}

// Name implements Source.
func (Auto) Name() string { return "auto" }

// Read implements Source.
func (Auto) Read(ctx context.Context, s Session) ([]Entry, error) {
	q, qUnmapped, qErr := readTableCounted(s.Client(), oidDot1qFdbPort, oidDot1qFdbStatus)
	if qErr == nil && len(q) > 0 && qUnmapped == 0 {
		return q, nil
	}
	b, bUnmapped, bErr := readTableCounted(s.Client(), oidDot1dFdbPort, oidDot1dFdbStatus)
	return pick(tableResult{q, qUnmapped, qErr}, tableResult{b, bUnmapped, bErr})
}

type tableResult struct {
	entries  []Entry
	unmapped int
	err      error
}

// pick chooses between the Q-BRIDGE and the BRIDGE-MIB result: Q-BRIDGE
// unless it failed, is empty or has more unmapped bridge ports than BRIDGE-MIB.
func pick(q, b tableResult) ([]Entry, error) {
	qOK := q.err == nil && len(q.entries) > 0
	if qOK && q.unmapped == 0 {
		return q.entries, nil
	}
	if b.err == nil && len(b.entries) > 0 && (!qOK || b.unmapped < q.unmapped) {
		return b.entries, nil
	}
	if qOK {
		return q.entries, nil
	}
	return b.entries, b.err
}

// QBridge reads dot1qTpFdbPort (index: FDB ID / VLAN, then MAC).
type QBridge struct{}

// Name implements Source.
func (QBridge) Name() string { return "qbridge" }

// Read implements Source.
func (QBridge) Read(ctx context.Context, s Session) ([]Entry, error) {
	return readTable(s.Client(), oidDot1qFdbPort, oidDot1qFdbStatus)
}

// Bridge reads dot1dTpFdbPort (index: MAC). Some vendors (e.g. TP-Link)
// prepend a VLAN ID to the index; the MAC is always the last six parts.
type Bridge struct{}

// Name implements Source.
func (Bridge) Name() string { return "bridge" }

// Read implements Source.
func (Bridge) Read(ctx context.Context, s Session) ([]Entry, error) {
	return readTable(s.Client(), oidDot1dFdbPort, oidDot1dFdbStatus)
}

// CiscoVLAN reads BRIDGE-MIB once per VLAN via community@vlan or v3 context
// vlan-<id>, as Cisco IOS only shows the FDB of one VLAN per context.
type CiscoVLAN struct{}

// Name implements Source.
func (CiscoVLAN) Name() string { return "cisco-vlan" }

// Read implements Source.
func (CiscoVLAN) Read(ctx context.Context, s Session) ([]Entry, error) {
	rows, err := snmp.WalkColumn(s.Client(), oidVtpVlanState)
	if err != nil {
		return nil, fmt.Errorf("walk vtpVlanState: %w", err)
	}
	var out []Entry
	for _, r := range rows {
		if len(r.Index) != 2 {
			continue
		}
		vlan := r.Index[1]
		if state, _ := snmp.Int(r.PDU); state != 1 || (vlan >= 1002 && vlan <= 1005) {
			continue
		}
		g, err := s.VLAN(ctx, vlan)
		if err != nil {
			return nil, err
		}
		entries, err := readTable(g, oidDot1dFdbPort, oidDot1dFdbStatus)
		snmp.Close(g)
		if err != nil {
			return nil, fmt.Errorf("vlan %d: %w", vlan, err)
		}
		for i := range entries {
			entries[i].VLAN = vlan
		}
		out = append(out, entries...)
	}
	return out, nil
}

// readTable walks an FDB port column, filters by status and maps bridge
// ports to ifIndex.
func readTable(g *gosnmp.GoSNMP, portCol, statusCol string) ([]Entry, error) {
	entries, _, err := readTableCounted(g, portCol, statusCol)
	return entries, err
}

// readTableCounted is readTable that also returns how many entries had a
// bridge port missing from dot1dBasePortIfIndex.
func readTableCounted(g *gosnmp.GoSNMP, portCol, statusCol string) ([]Entry, int, error) {
	ports, err := snmp.WalkColumn(g, portCol)
	if err != nil {
		return nil, 0, fmt.Errorf("walk FDB: %w", err)
	}
	if len(ports) == 0 {
		return nil, 0, nil
	}
	statusRows, err := snmp.WalkColumn(g, statusCol)
	if err != nil {
		return nil, 0, fmt.Errorf("walk FDB status: %w", err)
	}
	baseRows, err := snmp.WalkColumn(g, oidBasePortIfIndex)
	if err != nil {
		return nil, 0, fmt.Errorf("walk dot1dBasePortIfIndex: %w", err)
	}
	entries, unmapped := resolve(ports, statusRows, baseRows)
	return entries, unmapped, nil
}

// resolve turns the rows of an FDB port column, its status column and
// dot1dBasePortIfIndex into entries. unmapped counts entries whose bridge
// port is not in a non-empty dot1dBasePortIfIndex; their bridge port is used
// as ifIndex, which is right only for some switches.
func resolve(ports, statusRows, baseRows []snmp.Row) (entries []Entry, unmapped int) {
	status := map[string]int{}
	for _, r := range statusRows {
		v, _ := snmp.Int(r.PDU)
		status[key(r.Index)] = v
	}
	bridgeToIf := bridgePortMap(baseRows)
	for _, r := range ports {
		if len(r.Index) < 6 {
			continue
		}
		switch status[key(r.Index)] {
		case statusInvalid, statusSelf, statusMgmt:
			continue
		}
		mac, ok := macaddr.FromOID(r.Index[len(r.Index)-6:])
		if !ok || mac.IsMulticast() || mac.IsZero() {
			continue
		}
		bp, _ := snmp.Int(r.PDU)
		if bp == 0 {
			continue
		}
		ifIndex, ok := bridgeToIf[bp]
		if !ok {
			// Without dot1dBasePortIfIndex many switches use ifIndex directly.
			ifIndex = bp
			if len(bridgeToIf) > 0 {
				unmapped++
			}
		}
		vlan := 0
		if len(r.Index) == 7 {
			vlan = r.Index[0]
		}
		entries = append(entries, Entry{MAC: mac, IfIndex: ifIndex, VLAN: vlan})
	}
	return entries, unmapped
}

func bridgePortMap(rows []snmp.Row) map[int]int {
	out := make(map[int]int, len(rows))
	for _, r := range rows {
		if len(r.Index) != 1 {
			continue
		}
		v, _ := snmp.Int(r.PDU)
		out[r.Index[0]] = v
	}
	return out
}

func key(idx []int) string {
	var b strings.Builder
	for i, v := range idx {
		if i > 0 {
			b.WriteByte('.')
		}
		fmt.Fprint(&b, v)
	}
	return b.String()
}
