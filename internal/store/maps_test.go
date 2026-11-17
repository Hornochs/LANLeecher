package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lan-dot-party/lanleecher/internal/store"
)

func TestMaps(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	mk := func(name string) *store.Switch {
		sw := &store.Switch{Name: name, Host: "192.0.2.2", Port: 161, SNMPVersion: "v2c", CredentialsEnc: []byte{0},
			FDBAdapter: "auto", Enabled: true}
		if err := db.CreateSwitch(ctx, sw); err != nil {
			t.Fatal(err)
		}
		return sw
	}
	a, b := mk("a"), mk("b")
	wan := &store.WANLink{Name: "Internet"}
	if err := db.CreateWANLink(ctx, wan); err != nil {
		t.Fatal(err)
	}
	m := &store.Map{Name: "Hall 1"}
	if err := db.CreateMap(ctx, m); err != nil || m.Refresh != 15 {
		t.Fatalf("create: %v %+v", err, m)
	}
	if err := db.CreateMap(ctx, &store.Map{Name: "hall 1"}); !errors.Is(err, store.ErrExists) {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := db.CreateMap(ctx, &store.Map{Name: "x", Refresh: 1}); err == nil {
		t.Fatal("refresh 1 s accepted")
	}
	if err := db.SetMapBackground(ctx, m.ID, []byte("png bytes"), "image/png", 800, 600); err != nil {
		t.Fatal(err)
	}
	got, err := db.MapByID(ctx, m.ID)
	if err != nil || !got.HasBackground() || got.BGWidth != 800 || got.BGType != "image/png" {
		t.Fatalf("map %+v %v", got, err)
	}
	data, typ, hash, err := db.MapBackground(ctx, m.ID)
	if err != nil || string(data) != "png bytes" || typ != "image/png" || hash != got.BGHash {
		t.Fatalf("background %q %s %s %v", data, typ, hash, err)
	}
	if err := db.SetMapNodes(ctx, m.ID, []store.MapNode{{SwitchID: a.ID, X: 0.25, Y: 0.5}, {SwitchID: b.ID, X: 1.5, Y: -1}},
		[]store.MapWANNode{{WANLinkID: wan.ID, X: 0.5, Y: 0.1}}); err != nil {
		t.Fatal(err)
	}
	nodes, _ := db.MapNodes(ctx, m.ID)
	if len(nodes) != 2 || nodes[1].X != 1 || nodes[1].Y != 0 {
		t.Fatalf("nodes %+v", nodes)
	}
	if w, _ := db.MapWANNodes(ctx, m.ID); len(w) != 1 || w[0].WANLinkID != wan.ID {
		t.Fatalf("wan nodes %+v", w)
	}
	if err := db.SetMapNodes(ctx, m.ID, []store.MapNode{{SwitchID: a.ID, X: 0.1, Y: 0.1}, {SwitchID: 9999, X: 0.5, Y: 0.5}}, nil); err == nil {
		t.Fatal("unknown switch accepted")
	}
	if err := db.SetMapNodes(ctx, m.ID, nil, []store.MapWANNode{{WANLinkID: 9999}}); err == nil {
		t.Fatal("unknown WAN link accepted")
	}
	if nodes, _ := db.MapNodes(ctx, m.ID); len(nodes) != 2 {
		t.Fatalf("failed update changed the nodes: %+v", nodes)
	}
	l := &store.MapLink{Kind: store.MapLinkManual, ASwitch: a.ID, AIf: 1, BSwitch: b.ID, BIf: 2}
	if err := db.AddMapLink(ctx, m.ID, l); err != nil {
		t.Fatal(err)
	}
	if !l.Same(b.ID, 2, a.ID, 0) || l.Same(a.ID, 3, b.ID, 2) {
		t.Fatal("Same")
	}
	if err := db.AddMapLink(ctx, m.ID, &store.MapLink{Kind: "other", ASwitch: a.ID, BSwitch: b.ID}); err == nil {
		t.Fatal("bad kind accepted")
	}
	// Deleting a switch removes its nodes and links.
	if err := db.DeleteSwitch(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	nodes, _ = db.MapNodes(ctx, m.ID)
	links, _ := db.MapLinks(ctx, m.ID)
	if len(nodes) != 1 || len(links) != 0 {
		t.Fatalf("after delete: %+v %+v", nodes, links)
	}
	if err := db.DeleteMap(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MapByID(ctx, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted map: %v", err)
	}
}

func TestRecordTopology(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	sw := &store.Switch{Name: "sw", Host: "192.0.2.2", Port: 161, SNMPVersion: "v2c", CredentialsEnc: []byte{0},
		FDBAdapter: "auto", Enabled: true}
	if err := db.CreateSwitch(ctx, sw); err != nil {
		t.Fatal(err)
	}
	err := db.InTx(ctx, func(tx *store.Tx) error {
		_, err := tx.UpsertPorts(ctx, sw.ID, []store.PortObservation{{IfIndex: 1, IfName: "1"}, {IfIndex: 2, IfName: "2"}, {IfIndex: 9, IfName: "Po1"}}, store.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	record := func(chassis string, n map[int]store.LLDPNeighbor, lag map[int]int) {
		t.Helper()
		if err := db.InTx(ctx, func(tx *store.Tx) error {
			return tx.RecordTopology(ctx, sw.ID, chassis, "02:00:00:00:00:fe", n, lag, store.Now())
		}); err != nil {
			t.Fatal(err)
		}
	}
	record("02:00:00:00:00:fd", map[int]store.LLDPNeighbor{9: {ChassisID: "02:00:00:00:00:01", SysName: "core", PortID: "g1", Bridge: true}}, map[int]int{1: 9, 2: 9})
	nb, _ := db.ListLLDPNeighbors(ctx)
	if len(nb[sw.ID]) != 1 || nb[sw.ID][0].SysName != "core" || !nb[sw.ID][0].Bridge {
		t.Fatalf("neighbors %+v", nb)
	}
	got, _ := db.GetSwitch(ctx, sw.ID)
	if got.ChassisID != "02:00:00:00:00:fd" || got.BaseMAC != "02:00:00:00:00:fe" {
		t.Fatalf("switch %+v", got)
	}
	ports, _ := db.ListPorts(ctx, sw.ID)
	if ports[0].LAGIfIndex != 9 || ports[1].LAGIfIndex != 9 || ports[2].LAGIfIndex != 0 {
		t.Fatalf("lag %+v", ports)
	}
	// nil keeps, empty clears; an empty chassis ID keeps the stored one.
	record("", nil, nil)
	if nb, _ = db.ListLLDPNeighbors(ctx); len(nb[sw.ID]) != 1 {
		t.Fatal("nil neighbors cleared the data")
	}
	record("", map[int]store.LLDPNeighbor{}, map[int]int{2: 9})
	nb, _ = db.ListLLDPNeighbors(ctx)
	ports, _ = db.ListPorts(ctx, sw.ID)
	got, _ = db.GetSwitch(ctx, sw.ID)
	if len(nb[sw.ID]) != 0 || ports[0].LAGIfIndex != 0 || ports[1].LAGIfIndex != 9 || got.ChassisID != "02:00:00:00:00:fd" {
		t.Fatalf("after clearing: %+v %+v %+v", nb, ports, got)
	}
}
