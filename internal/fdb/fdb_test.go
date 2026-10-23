package fdb

import (
	"errors"
	"testing"

	"github.com/gosnmp/gosnmp"

	"github.com/lan-dot-party/lanleecher/internal/snmp"
)

func intRow(v int, idx ...int) snmp.Row {
	return snmp.Row{Index: idx, PDU: gosnmp.SnmpPDU{Type: gosnmp.Integer, Value: v}}
}

// fdbRow builds a row indexed <vlan>.<mac> (TP-Link style) for MAC 24:2f:d0:ec:c9:<last>.
func fdbRow(vlan, last, port int) snmp.Row {
	return intRow(port, vlan, 0x24, 0x2f, 0xd0, 0xec, 0xc9, last)
}

func TestResolve(t *testing.T) {
	// dot1dBasePortIfIndex as reported by a TP-Link JetStream: indexed by ifIndex.
	jetstreamBase := []snmp.Row{intRow(49153, 49153), intRow(49160, 49160)}
	tests := []struct {
		name         string
		ports        []snmp.Row
		status       []snmp.Row
		base         []snmp.Row
		wantIfIndex  []int
		wantUnmapped int
	}{
		{
			name:        "bridge port mapped via dot1dBasePortIfIndex",
			ports:       []snmp.Row{fdbRow(100, 1, 8)},
			base:        []snmp.Row{intRow(49160, 8)},
			wantIfIndex: []int{49160},
		},
		{
			name:        "no dot1dBasePortIfIndex: bridge port is ifIndex",
			ports:       []snmp.Row{fdbRow(100, 1, 49157)},
			wantIfIndex: []int{49157},
		},
		{
			name:         "JetStream Q-BRIDGE: port numbers missing from the map",
			ports:        []snmp.Row{fdbRow(100, 1, 8), fdbRow(7, 2, 8)},
			base:         jetstreamBase,
			wantIfIndex:  []int{8, 8},
			wantUnmapped: 2,
		},
		{
			name:        "JetStream BRIDGE-MIB: ifIndex values are in the map",
			ports:       []snmp.Row{fdbRow(100, 1, 49160)},
			base:        jetstreamBase,
			wantIfIndex: []int{49160},
		},
		{
			name:  "self, mgmt and invalid entries are skipped",
			ports: []snmp.Row{fdbRow(1, 1, 8), fdbRow(1, 2, 8), fdbRow(1, 3, 8), fdbRow(1, 4, 8)},
			status: []snmp.Row{
				intRow(statusSelf, 1, 0x24, 0x2f, 0xd0, 0xec, 0xc9, 1),
				intRow(statusMgmt, 1, 0x24, 0x2f, 0xd0, 0xec, 0xc9, 2),
				intRow(statusInvalid, 1, 0x24, 0x2f, 0xd0, 0xec, 0xc9, 3),
				intRow(3, 1, 0x24, 0x2f, 0xd0, 0xec, 0xc9, 4),
			},
			wantIfIndex: []int{8},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, unmapped := resolve(tt.ports, tt.status, tt.base)
			if unmapped != tt.wantUnmapped {
				t.Errorf("unmapped = %d, want %d", unmapped, tt.wantUnmapped)
			}
			if len(got) != len(tt.wantIfIndex) {
				t.Fatalf("got %d entries, want %d: %+v", len(got), len(tt.wantIfIndex), got)
			}
			for i, e := range got {
				if e.IfIndex != tt.wantIfIndex[i] {
					t.Errorf("entry %d: ifIndex %d, want %d", i, e.IfIndex, tt.wantIfIndex[i])
				}
			}
		})
	}
}

func TestResolveVLANAndMAC(t *testing.T) {
	got, _ := resolve([]snmp.Row{fdbRow(1337, 0x60, 8)}, nil, nil)
	if len(got) != 1 || got[0].VLAN != 1337 || got[0].MAC.String() != "24:2f:d0:ec:c9:60" {
		t.Fatalf("got %+v", got)
	}
}

func TestPick(t *testing.T) {
	q := []Entry{{IfIndex: 8}}
	b := []Entry{{IfIndex: 49160}}
	errWalk := errors.New("timeout")
	tests := []struct {
		name string
		q, b tableResult
		want []Entry
		err  bool
	}{
		{"Q-BRIDGE fully mapped", tableResult{entries: q}, tableResult{entries: b}, q, false},
		{"Q-BRIDGE unmapped, BRIDGE-MIB mapped (JetStream)", tableResult{entries: q, unmapped: 1}, tableResult{entries: b}, b, false},
		{"both equally unmapped: keep Q-BRIDGE", tableResult{entries: q, unmapped: 1}, tableResult{entries: b, unmapped: 1}, q, false},
		{"Q-BRIDGE unmapped, BRIDGE-MIB empty", tableResult{entries: q, unmapped: 1}, tableResult{}, q, false},
		{"Q-BRIDGE unmapped, BRIDGE-MIB fails", tableResult{entries: q, unmapped: 1}, tableResult{err: errWalk}, q, false},
		{"Q-BRIDGE empty", tableResult{}, tableResult{entries: b}, b, false},
		{"Q-BRIDGE fails", tableResult{err: errWalk}, tableResult{entries: b}, b, false},
		{"both empty", tableResult{}, tableResult{}, nil, false},
		{"Q-BRIDGE empty, BRIDGE-MIB fails", tableResult{}, tableResult{err: errWalk}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pick(tt.q, tt.b)
			if (err != nil) != tt.err {
				t.Fatalf("err = %v, want error %v", err, tt.err)
			}
			if len(got) != len(tt.want) || (len(got) > 0 && got[0].IfIndex != tt.want[0].IfIndex) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
