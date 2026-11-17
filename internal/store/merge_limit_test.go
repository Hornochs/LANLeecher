package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/store"
)

func TestListMergeSuggestionsLimit(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	ev, err := db.StartEvent(ctx, "lan", "")
	if err != nil {
		t.Fatal(err)
	}
	n := store.MergeSuggestionLimit + 20
	for i := 0; i < n; i++ {
		a, b := fmt.Sprintf("02:00:00:01:%02x:%02x", i>>8, i&0xff), fmt.Sprintf("02:00:00:02:%02x:%02x", i>>8, i&0xff)
		for _, m := range []string{a, b} {
			if err := db.EnsureMAC(ctx, m, store.Now()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.CreateMergeSuggestion(ctx, a, b, "hostname:x", ev.ID); err != nil {
			t.Fatal(err)
		}
	}
	list, truncated, err := db.ListMergeSuggestions(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != store.MergeSuggestionLimit || !truncated {
		t.Fatalf("got %d suggestions, truncated=%v; want %d, true", len(list), truncated, store.MergeSuggestionLimit)
	}
}

func TestMergeSuggestionShowsPortAndVendor(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	sw := &store.Switch{Name: "sw1", Host: "192.0.2.2", Port: 161, SNMPVersion: "v2c", CredentialsEnc: []byte{0},
		FDBAdapter: "auto", Enabled: true}
	if err := db.CreateSwitch(ctx, sw); err != nil {
		t.Fatal(err)
	}
	ev, err := db.StartEvent(ctx, "lan", "")
	if err != nil {
		t.Fatal(err)
	}
	const a, b = "02:00:00:00:00:01", "02:00:00:00:00:02"
	err = db.InTx(ctx, func(tx *store.Tx) error {
		ports, err := tx.UpsertPorts(ctx, sw.ID, []store.PortObservation{
			{IfIndex: 1, IfName: "Gi1/0/1", OperUp: true, HCSupported: true, Physical: true},
		}, store.Now())
		if err != nil {
			return err
		}
		return tx.RecordSightings(ctx, sw.ID, []store.Sighting{{MAC: a, PortID: ports[1].ID, VLAN: 1}}, nil, 8, store.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureMAC(ctx, b, store.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateMergeSuggestion(ctx, a, b, "hostname:x", ev.ID); err != nil {
		t.Fatal(err)
	}
	list, _, err := db.ListMergeSuggestions(ctx, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if list[0].PortA != "sw1 Gi1/0/1" || list[0].PortB != "" {
		t.Fatalf("ports = %q / %q, want %q / empty", list[0].PortA, list[0].PortB, "sw1 Gi1/0/1")
	}
}

func TestOnlineTogether(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	for m, at := range map[string]time.Time{"02:00:00:00:00:01": store.Now(), "02:00:00:00:00:02": store.Now(),
		"02:00:00:00:00:03": store.Now().Add(-time.Hour)} {
		if err := db.EnsureMAC(ctx, m, at); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		a, b string
		want bool
	}{{"02:00:00:00:00:01", "02:00:00:00:00:02", true}, {"02:00:00:00:00:01", "02:00:00:00:00:03", false}} {
		got, err := db.OnlineTogether(ctx, tt.a, tt.b, 2*time.Minute)
		if err != nil || got != tt.want {
			t.Errorf("OnlineTogether(%s, %s) = %v, %v; want %v", tt.a, tt.b, got, err, tt.want)
		}
	}
}
