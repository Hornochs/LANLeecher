package store_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "store.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestUplinkDismissed(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	sw := &store.Switch{Name: "sw1", Host: "192.0.2.2", Port: 161, SNMPVersion: "v2c", CredentialsEnc: []byte{0},
		FDBAdapter: "auto", Enabled: true}
	if err := db.CreateSwitch(ctx, sw); err != nil {
		t.Fatal(err)
	}
	// A hypervisor port with more MACs than the threshold.
	sightings := func(portID int64) []store.Sighting {
		var out []store.Sighting
		for i := 0; i < 4; i++ {
			out = append(out, store.Sighting{MAC: "02:00:00:00:00:0" + string(rune('1'+i)), PortID: portID, VLAN: 1})
		}
		return out
	}
	const threshold = 2
	var portID int64
	poll := func() *store.Port {
		t.Helper()
		err := db.InTx(ctx, func(tx *store.Tx) error {
			ports, err := tx.UpsertPorts(ctx, sw.ID, []store.PortObservation{
				{IfIndex: 1, IfName: "Gi1/0/1", OperUp: true, HCSupported: true, Physical: true},
			}, store.Now())
			if err != nil {
				return err
			}
			portID = ports[1].ID
			return tx.RecordSightings(ctx, sw.ID, sightings(portID), nil, threshold, store.Now())
		})
		if err != nil {
			t.Fatal(err)
		}
		p, err := db.GetPort(ctx, portID)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	set := func(s store.PortSettings) {
		t.Helper()
		s.UplinkThreshold = threshold
		if err := db.UpdatePorts(ctx, []int64{portID}, s); err != nil {
			t.Fatal(err)
		}
	}
	yes, no := true, false

	if p := poll(); !p.UplinkSuspect || p.UplinkDismissed {
		t.Fatalf("new port: suspect=%v dismissed=%v, want true/false", p.UplinkSuspect, p.UplinkDismissed)
	}
	set(store.PortSettings{UplinkDismissed: &yes})
	if p, _ := db.GetPort(ctx, portID); p.UplinkSuspect || !p.UplinkDismissed {
		t.Fatalf("after dismiss: suspect=%v dismissed=%v", p.UplinkSuspect, p.UplinkDismissed)
	}
	if p := poll(); p.UplinkSuspect {
		t.Fatal("hint is back after the next forwarding-table read")
	}
	set(store.PortSettings{UplinkDismissed: &no})
	if p, _ := db.GetPort(ctx, portID); !p.UplinkSuspect || p.UplinkDismissed {
		t.Fatalf("after restore: suspect=%v dismissed=%v, want true/false", p.UplinkSuspect, p.UplinkDismissed)
	}
	// Making the port an uplink drops the confirmation.
	set(store.PortSettings{UplinkDismissed: &yes})
	uplink := accounting.RoleUplink
	set(store.PortSettings{Role: &uplink})
	if p := poll(); p.UplinkSuspect || p.UplinkDismissed {
		t.Fatalf("uplink: suspect=%v dismissed=%v, want false/false", p.UplinkSuspect, p.UplinkDismissed)
	}
}

func TestWindowTotals(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	sw := &store.Switch{Name: "sw1", Host: "192.0.2.2", Port: 161, SNMPVersion: "v2c", CredentialsEnc: []byte{0},
		FDBAdapter: "auto", Enabled: true}
	if err := db.CreateSwitch(ctx, sw); err != nil {
		t.Fatal(err)
	}
	ev, err := db.StartEvent(ctx, "LAN", "")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 24, 18, 0, 0, 0, time.UTC)
	err = db.InTx(ctx, func(tx *store.Tx) error {
		ports, err := tx.UpsertPorts(ctx, sw.ID, []store.PortObservation{
			{IfIndex: 1, IfName: "Gi1/0/1", OperUp: true, HCSupported: true, Physical: true},
		}, t0)
		if err != nil {
			return err
		}
		p := ports[1].ID
		if err := tx.RecordSightings(ctx, sw.ID, []store.Sighting{{MAC: "02:00:00:00:00:01", PortID: p, VLAN: 1}},
			nil, 10, t0); err != nil {
			return err
		}
		for _, b := range []struct {
			typ, id  string
			at       time.Time
			down, up uint64
		}{
			{store.SubjectMAC, "02:00:00:00:00:01", t0.Add(1 * time.Minute), 100, 10},
			{store.SubjectUnassigned, "1", t0.Add(2 * time.Minute), 5, 1},
			{store.SubjectMAC, "02:00:00:00:00:01", t0.Add(6 * time.Minute), 1000, 100}, // next bucket
		} {
			if err := tx.AddSubjectTraffic(ctx, ev.ID, b.typ, b.id, p, b.down, b.up, b.at); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	all, unassigned, err := db.WindowTotals(ctx, ev.ID, t0, t0.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if all.BytesDown != 105 || all.BytesUp != 11 || unassigned.BytesDown != 5 || unassigned.BytesUp != 1 {
		t.Fatalf("first bucket: all=%+v unassigned=%+v", all, unassigned)
	}
	rows, err := db.Rankings(ctx, store.RankingQuery{EventID: ev.ID, From: t0, To: t0.Add(10 * time.Minute),
		Order: "total", Limit: 10, IncludeHidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].BytesDown != 1100 {
		t.Fatalf("rankings over two buckets: %+v", rows)
	}
}
