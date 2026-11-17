package topology

import (
	"reflect"
	"testing"
)

func port(ifIndex int, name string, nb *Neighbor) *Port {
	return &Port{IfIndex: ifIndex, Name: name, Neighbor: nb}
}

func snap(name, mac string, fdb []FDBEntry, ports ...*Port) *Snapshot {
	return &Snapshot{System: System{Name: name, ChassisID: mac}, Ports: ports, FDB: fdb}
}

func nb(mac, name, portID string) *Neighbor {
	return &Neighbor{ChassisID: mac, SysName: name, PortID: portID}
}

const (
	macA = "02:00:00:00:00:0a"
	macB = "02:00:00:00:00:0b"
	macC = "02:00:00:00:00:0c"
)

func TestLLDPChain(t *testing.T) {
	devs := []Device{
		{ID: 3, Name: "c", Snap: snap("c", macC, nil, port(1, "1/1", nb(macB, "b", "1/2")))},
		{ID: 1, Name: "a", Snap: snap("a", macA, nil, port(1, "1/1", nb(macB, "b", "1/1")), port(2, "1/2", nb("02:00:00:ff:ff:ff", "unknown", "x")))},
		{ID: 2, Name: "b", Snap: snap("b", macB, nil, port(1, "1/1", nb(macA, "a", "1/1")), port(2, "1/2", nb(macC, "c", "1/1")))},
	}
	want := []Link{
		{A: End{1, 1, "1/1"}, B: End{2, 1, "1/1"}, Source: "lldp"},
		{A: End{2, 2, "1/2"}, B: End{3, 1, "1/1"}, Source: "lldp"},
	}
	if got := Build(devs); !reflect.DeepEqual(got, want) {
		t.Errorf("links %+v", got)
	}
}

func TestLAGGivesOneLink(t *testing.T) {
	lag := func(ifIndex, lagIf int, name string, n *Neighbor) *Port {
		p := port(ifIndex, name, n)
		p.LAG, p.LAGName = lagIf, "Po1"
		return p
	}
	la := &Port{IfIndex: 100, Name: "Po1"}
	lb := &Port{IfIndex: 200, Name: "Po9"}
	a := snap("a", macA, nil, lag(7, 100, "gi7", nb(macB, "b", "gi17")), lag(8, 100, "gi8", nb(macB, "b", "gi18")), la)
	b := snap("b", macB, nil, lag(17, 200, "gi17", nil), lag(18, 200, "gi18", nil), lb)
	b.Ports[0].LAGName, b.Ports[1].LAGName = "Po9", "Po9"
	got := Build([]Device{{ID: 1, Name: "a", Snap: a}, {ID: 2, Name: "b", Snap: b}})
	want := []Link{{A: End{1, 100, "Po1"}, B: End{2, 200, "Po9"}, Source: "lldp"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links %+v", got)
	}
}

func TestSeenFromBothSidesAndFarPortByReverse(t *testing.T) {
	// a names a port that b does not have; b sees a on port 3.
	a := snap("a", macA, nil, port(1, "1/1", nb(macB, "b", "no-such-port")))
	b := snap("b", macB, nil, port(3, "1/3", nb(macA, "a", "1/1")))
	got := Build([]Device{{ID: 2, Name: "b", Snap: b}, {ID: 1, Name: "a", Snap: a}})
	want := []Link{{A: End{1, 1, "1/1"}, B: End{2, 3, "1/3"}, Source: "lldp"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links %+v", got)
	}
}

func TestMatchBySysNameOnly(t *testing.T) {
	a := snap("a", "", nil, port(1, "1/1", nb("", "B-Switch", "1/4")))
	b := snap("b-switch", "", nil, port(4, "1/4", nil))
	got := Build([]Device{{ID: 1, Name: "a", Snap: a}, {ID: 2, Name: "x", Snap: b}})
	want := []Link{{A: End{1, 1, "1/1"}, B: End{2, 4, "1/4"}, Source: "lldp"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links %+v", got)
	}
	// By the device's own name when the snapshot has no sysName.
	b.System.Name = ""
	got = Build([]Device{{ID: 1, Name: "a", Snap: a}, {ID: 2, Name: "b-switch", Snap: b}})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links by device name %+v", got)
	}
}

func TestFDBLinkForSwitchWithoutLLDP(t *testing.T) {
	a := snap("a", macA, []FDBEntry{{MAC: macB, IfIndex: 5}, {MAC: "02:00:00:00:99:01", IfIndex: 6}},
		port(5, "1/5", nil), port(6, "1/6", nil))
	b := snap("b", macB, []FDBEntry{{MAC: macA, IfIndex: 2}}, port(2, "1/2", nil))
	got := Build([]Device{{ID: 1, Name: "a", Snap: a}, {ID: 2, Name: "b", Snap: b}})
	want := []Link{{A: End{1, 5, "1/5"}, B: End{2, 2, "1/2"}, Source: "fdb"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links %+v", got)
	}
	// Only one side has a table: far port unknown.
	b.FDB = nil
	got = Build([]Device{{ID: 1, Name: "a", Snap: a}, {ID: 2, Name: "b", Snap: b}})
	want = []Link{{A: End{1, 5, "1/5"}, B: End{DeviceID: 2}, Source: "fdb"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links without far table %+v", got)
	}
}

func TestFDBChainWithoutLLDP(t *testing.T) {
	// a - b - c: a also learns c on its port towards b, but c is behind b.
	a := snap("a", macA, []FDBEntry{{MAC: macB, IfIndex: 1}, {MAC: macC, IfIndex: 1}}, port(1, "1/1", nil))
	b := snap("b", macB, []FDBEntry{{MAC: macA, IfIndex: 1}, {MAC: macC, IfIndex: 2}}, port(1, "1/1", nil), port(2, "1/2", nil))
	c := snap("c", macC, []FDBEntry{{MAC: macA, IfIndex: 1}, {MAC: macB, IfIndex: 1}}, port(1, "1/1", nil))
	got := Build([]Device{{ID: 1, Snap: a}, {ID: 2, Snap: b}, {ID: 3, Snap: c}})
	want := []Link{
		{A: End{1, 1, "1/1"}, B: End{2, 1, "1/1"}, Source: "fdb"},
		{A: End{2, 2, "1/2"}, B: End{3, 1, "1/1"}, Source: "fdb"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links %+v", got)
	}
}

func TestFDBIgnoredWhenLLDPLinkExists(t *testing.T) {
	a := snap("a", macA, []FDBEntry{{MAC: macB, IfIndex: 2}}, port(1, "1/1", nb(macB, "b", "1/1")), port(2, "1/2", nil))
	b := snap("b", macB, []FDBEntry{{MAC: macA, IfIndex: 1}}, port(1, "1/1", nil))
	got := Build([]Device{{ID: 1, Snap: a}, {ID: 2, Snap: b}})
	if len(got) != 1 || got[0].Source != "lldp" {
		t.Errorf("links %+v", got)
	}
}

func TestEmptyAndSelf(t *testing.T) {
	if got := Build(nil); len(got) != 0 {
		t.Errorf("nil: %+v", got)
	}
	devs := []Device{{ID: 1, Name: "a"}, {ID: 2, Name: "b", Snap: &Snapshot{}},
		{ID: 3, Name: "c", Snap: snap("c", macC, []FDBEntry{{MAC: macC, IfIndex: 1}}, port(1, "1/1", nb(macC, "c", "1/1")))}}
	if got := Build(devs); len(got) != 0 {
		t.Errorf("empty/self: %+v", got)
	}
	// A neighbour that is a device without a snapshot still gives a link.
	a := snap("a", macA, nil, port(1, "1/1", nb("", "nosnap", "x")))
	got := Build([]Device{{ID: 1, Name: "a", Snap: a}, {ID: 2, Name: "nosnap"}})
	want := []Link{{A: End{1, 1, "1/1"}, B: End{DeviceID: 2}, Source: "lldp"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links %+v", got)
	}
}

func TestLLDPRing(t *testing.T) {
	// a:1 - b:1, b:2 - c:1, c:2 - a:2
	a := snap("a", macA, nil, port(1, "1/1", nb(macB, "b", "1/1")), port(2, "1/2", nb(macC, "c", "1/2")))
	b := snap("b", macB, nil, port(1, "1/1", nb(macA, "a", "1/1")), port(2, "1/2", nb(macC, "c", "1/1")))
	c := snap("c", macC, nil, port(1, "1/1", nb(macB, "b", "1/2")), port(2, "1/2", nb(macA, "a", "1/2")))
	got := Build([]Device{{ID: 1, Snap: a}, {ID: 2, Snap: b}, {ID: 3, Snap: c}})
	want := []Link{
		{A: End{1, 1, "1/1"}, B: End{2, 1, "1/1"}, Source: "lldp"},
		{A: End{1, 2, "1/2"}, B: End{3, 2, "1/2"}, Source: "lldp"},
		{A: End{2, 2, "1/2"}, B: End{3, 1, "1/1"}, Source: "lldp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links %+v", got)
	}
}

func TestFDBPicksPortWithFewestMACs(t *testing.T) {
	// b's MAC is learned on port 1 (uplink with many hosts) and port 2 (direct).
	a := snap("a", macA, []FDBEntry{
		{MAC: macB, IfIndex: 1}, {MAC: "02:00:00:00:99:01", IfIndex: 1}, {MAC: "02:00:00:00:99:02", IfIndex: 1},
		{MAC: macB, IfIndex: 2},
	}, port(1, "1/1", nil), port(2, "1/2", nil))
	b := snap("b", macB, nil, port(1, "1/1", nil))
	got := Build([]Device{{ID: 1, Snap: a}, {ID: 2, Snap: b}})
	want := []Link{{A: End{1, 2, "1/2"}, B: End{DeviceID: 2}, Source: "fdb"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links %+v", got)
	}
}
