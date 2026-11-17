package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

func TestWANLinks(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	sw := &store.Switch{Name: "core", Host: "192.0.2.1", Port: 161, SNMPVersion: "v2c", CredentialsEnc: []byte{0},
		FDBAdapter: "auto", Enabled: true}
	if err := db.CreateSwitch(ctx, sw); err != nil {
		t.Fatal(err)
	}
	fiber := &store.WANLink{Name: "Fiber", DownMbps: 1000, UpMbps: 500}
	sat := &store.WANLink{Name: "Satellite", Sort: 1}
	for _, l := range []*store.WANLink{fiber, sat} {
		if err := db.CreateWANLink(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CreateWANLink(ctx, &store.WANLink{Name: "fiber"}); !errors.Is(err, store.ErrWANLinkExists) {
		t.Fatalf("duplicate name (case-insensitive): %v", err)
	}
	if err := db.CreateWANLink(ctx, &store.WANLink{Name: " "}); !errors.Is(err, store.ErrWANLinkInvalid) {
		t.Fatalf("empty name: %v", err)
	}

	ev, err := db.StartEvent(ctx, "LAN", "")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	var ports map[int]*store.Port
	err = db.InTx(ctx, func(tx *store.Tx) error {
		var err error
		ports, err = tx.UpsertPorts(ctx, sw.ID, []store.PortObservation{
			{IfIndex: 1, IfName: "1", OperUp: true, HCSupported: true, Physical: true, DefaultRole: "client"},
			{IfIndex: 2, IfName: "2", OperUp: true, HCSupported: true, Physical: true, DefaultRole: "client"},
			{IfIndex: 3, IfName: "3", OperUp: true, HCSupported: true, Physical: true, DefaultRole: "client"},
		}, at)
		if err != nil {
			return err
		}
		// Booked in client view: router sent 900 into port 1 (port in = stored up).
		for ifIndex, v := range map[int][2]uint64{1: {100, 900}, 2: {10, 90}, 3: {1, 9}} {
			for i := 0; i < 2; i++ {
				if err := tx.AddPortTraffic(ctx, ev.ID, ports[ifIndex].ID, v[0], v[1], at.Add(time.Duration(i)*5*time.Minute)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	wan := accounting.RoleWAN
	if err := db.UpdatePorts(ctx, []int64{ports[1].ID}, store.PortSettings{Role: &wan}); !errors.Is(err, store.ErrWANLinkRequired) {
		t.Fatalf("wan without link: %v", err)
	}
	bogus := int64(999)
	if err := db.UpdatePorts(ctx, []int64{ports[1].ID}, store.PortSettings{Role: &wan, WANLinkID: &bogus}); !errors.Is(err, store.ErrWANLinkRequired) {
		t.Fatalf("wan with unknown link: %v", err)
	}
	// Assigned after the traffic was booked: still reported.
	if err := db.UpdatePorts(ctx, []int64{ports[1].ID}, store.PortSettings{Role: &wan, WANLinkID: &fiber.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdatePorts(ctx, []int64{ports[2].ID, ports[3].ID}, store.PortSettings{Role: &wan, WANLinkID: &sat.ID}); err != nil {
		t.Fatal(err)
	}

	got, err := db.WANTraffic(ctx, ev.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Link.Name != "Fiber" || got[1].Link.Name != "Satellite" {
		t.Fatalf("links: %+v", got)
	}
	if got[0].InternetDown != 1800 || got[0].InternetUp != 200 || len(got[0].Ports) != 1 {
		t.Fatalf("fiber: %+v", got[0])
	}
	if got[1].InternetDown != 2*(90+9) || got[1].InternetUp != 2*(10+1) || len(got[1].Ports) != 2 {
		t.Fatalf("satellite: %+v", got[1])
	}
	if len(got[0].Series) != 2 || got[0].Series[0].BytesDown != 900 {
		t.Fatalf("fiber series: %+v", got[0].Series)
	}

	// WAN traffic is not part of the event's client totals.
	sum, err := db.EventSummary(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.BytesDown != 0 || sum.BytesUp != 0 {
		t.Fatalf("event summary counts WAN ports: %+v", sum)
	}

	if err := db.DeleteWANLink(ctx, sat.ID); !errors.Is(err, store.ErrWANLinkInUse) {
		t.Fatalf("delete in use: %v", err)
	}
	client := accounting.RoleClient
	if err := db.UpdatePorts(ctx, []int64{ports[2].ID, ports[3].ID}, store.PortSettings{Role: &client}); err != nil {
		t.Fatal(err)
	}
	p, _ := db.GetPort(ctx, ports[2].ID)
	if p.WANLinkID != 0 {
		t.Fatal("non-WAN port kept its WAN link")
	}
	if err := db.DeleteWANLink(ctx, sat.ID); err != nil {
		t.Fatal(err)
	}
}
