package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/store"
)

func floodSetup(t *testing.T) (*store.DB, *store.Switch, map[int]int64) {
	t.Helper()
	ctx := context.Background()
	db := openDB(t)
	sw := &store.Switch{Name: "sw1", Host: "192.0.2.2", Port: 161, SNMPVersion: "v2c", CredentialsEnc: []byte{0},
		FDBAdapter: "auto", Enabled: true}
	if err := db.CreateSwitch(ctx, sw); err != nil {
		t.Fatal(err)
	}
	ids := map[int]int64{}
	err := db.InTx(ctx, func(tx *store.Tx) error {
		ports, err := tx.UpsertPorts(ctx, sw.ID, []store.PortObservation{
			{IfIndex: 1, IfName: "Gi1/0/1", OperUp: true, HCSupported: true, Physical: true},
			{IfIndex: 2, IfName: "Gi1/0/2", OperUp: true, HCSupported: true, Physical: true},
		}, store.Now())
		for i, p := range ports {
			ids[i] = p.ID
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, sw, ids
}

func record(t *testing.T, db *store.DB, sw *store.Switch, s []store.Sighting, lldp map[int64]store.PortNeighbor,
	opt store.SightingOptions, at time.Time) []int64 {
	t.Helper()
	var flooded []int64
	err := db.InTx(context.Background(), func(tx *store.Tx) error {
		var err error
		flooded, err = tx.RecordSightings(context.Background(), sw.ID, s, lldp, opt, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return flooded
}

func manyMACs(port int64, from, n int) []store.Sighting {
	var out []store.Sighting
	for i := from; i < from+n; i++ {
		out = append(out, store.Sighting{MAC: fmt.Sprintf("02:00:00:%02x:%02x:%02x", i>>16&0xff, i>>8&0xff, i&0xff),
			PortID: port, VLAN: 1})
	}
	return out
}

// A flooded port creates no new devices, but known MACs keep being updated.
func TestMACFloodCap(t *testing.T) {
	ctx := context.Background()
	db, sw, ports := floodSetup(t)
	opt := store.SightingOptions{UplinkThreshold: 32, MaxPortMACs: 50}
	t0 := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)

	// Port 1: one known client. Port 2: a normal table.
	record(t, db, sw, append(manyMACs(ports[1], 0, 1), manyMACs(ports[2], 1000, 10)...), nil, opt, t0)

	// Now port 1 floods with 200 MACs (the known one among them).
	flooded := record(t, db, sw, append(manyMACs(ports[1], 0, 200), manyMACs(ports[2], 1000, 10)...), nil, opt, t0.Add(time.Minute))
	if len(flooded) != 1 || flooded[0] != ports[1] {
		t.Fatalf("flooded = %v, want [%d]", flooded, ports[1])
	}
	known, err := db.MACKnown(ctx, "02:00:00:00:00:05")
	if err != nil || known {
		t.Fatalf("MAC from a flooded port was created (known=%v err=%v)", known, err)
	}
	if known, _ := db.MACKnown(ctx, "02:00:00:00:00:00"); !known {
		t.Fatal("known MAC on the flooded port was lost")
	}
	if p, _ := db.GetPort(ctx, ports[1]); p.MACCount != 200 {
		t.Errorf("mac_count = %d, want 200 (the count still shows the flood)", p.MACCount)
	}
	if known, _ := db.MACKnown(ctx, "02:00:00:00:03:ef"); !known {
		t.Error("normal port stopped creating MACs")
	}
}

// Only a bridge as LLDP neighbour counts, and only after several reads.
func TestUplinkHintIsMild(t *testing.T) {
	ctx := context.Background()
	db, sw, ports := floodSetup(t)
	opt := store.SightingOptions{UplinkThreshold: 32}
	t0 := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)
	lldp := map[int64]store.PortNeighbor{
		ports[1]: {Name: "pc-with-lldpd"},               // a PC: never a hint
		ports[2]: {Name: "access-switch", Bridge: true}, // a switch
	}
	for i := 1; i <= store.UplinkConfirmReads; i++ {
		record(t, db, sw, manyMACs(ports[1], 0, 20), lldp, opt, t0.Add(time.Duration(i)*time.Minute))
		p1, _ := db.GetPort(ctx, ports[1])
		p2, _ := db.GetPort(ctx, ports[2])
		if p1.UplinkSuspect {
			t.Fatalf("read %d: port with 20 MACs and a non-bridge LLDP neighbour got the hint", i)
		}
		if want := i >= store.UplinkConfirmReads; p2.UplinkSuspect != want {
			t.Fatalf("read %d: bridge port suspect=%v, want %v", i, p2.UplinkSuspect, want)
		}
		if p1.LLDPNeighbor != "pc-with-lldpd" {
			t.Fatalf("LLDP neighbour name not stored: %q", p1.LLDPNeighbor)
		}
	}
	// One normal read resets the streak; the hint goes away.
	record(t, db, sw, manyMACs(ports[1], 0, 1), nil, opt, t0.Add(time.Hour))
	if p2, _ := db.GetPort(ctx, ports[2]); p2.UplinkSuspect {
		t.Fatal("hint stays after a normal read")
	}
	// Many MACs count as well, again only after several reads.
	for i := 1; i <= store.UplinkConfirmReads; i++ {
		record(t, db, sw, manyMACs(ports[1], 0, 40), nil, opt, t0.Add(time.Hour+time.Duration(i)*time.Minute))
	}
	if p1, _ := db.GetPort(ctx, ports[1]); !p1.UplinkSuspect {
		t.Fatal("port with 40 MACs in several reads has no hint")
	}
}

func TestPurgeIdleMACs(t *testing.T) {
	ctx := context.Background()
	db, sw, ports := floodSetup(t)
	ev, err := db.StartEvent(ctx, "LAN", "")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	opt := store.SightingOptions{UplinkThreshold: 32}
	// idle: 02:..:00; with traffic: 02:..:01; named device: 02:..:02; recent: port 2.
	record(t, db, sw, manyMACs(ports[1], 0, 3), nil, opt, old)
	record(t, db, sw, manyMACs(ports[2], 10, 1), nil, opt, time.Now())
	if err := db.InTx(ctx, func(tx *store.Tx) error {
		return tx.AddSubjectTraffic(ctx, ev.ID, store.SubjectMAC, "02:00:00:00:00:01", ports[1], 100, 10, old)
	}); err != nil {
		t.Fatal(err)
	}
	dev, err := db.DeviceOfMAC(ctx, "02:00:00:00:00:02")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDevice(ctx, dev, "Gamer PC", false, ""); err != nil {
		t.Fatal(err)
	}

	n, err := db.PurgeIdleMACs(ctx, time.Now().Add(-24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("purged %d (err %v), want 1", n, err)
	}
	for mac, want := range map[string]bool{
		"02:00:00:00:00:00": false, // idle, no traffic
		"02:00:00:00:00:01": true,  // had traffic
		"02:00:00:00:00:02": true,  // named device
		"02:00:00:00:00:0a": true,  // seen recently
	} {
		if got, _ := db.MACKnown(ctx, mac); got != want {
			t.Errorf("%s known=%v, want %v", mac, got, want)
		}
	}
}
