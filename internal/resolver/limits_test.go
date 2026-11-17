package resolver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/config"
	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

// newTestService returns a service with the netbios prober (never run, so no
// packets are sent) and an empty database.
func newTestService(t *testing.T) (*Service, *store.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := db.Migrate(ctx, log); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Resolver
	db.HostnamePriority = cfg.HostnamePriority
	cfg.Order = []string{"snmp-arp", "netbios"}
	return New(db, cfg, log), db
}

func mustMAC(t *testing.T, s string) macaddr.MAC {
	t.Helper()
	m, err := macaddr.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestObserveARPCapsNewPairsPerMAC(t *testing.T) {
	ctx := context.Background()
	s, db := newTestService(t)
	flood := mustMAC(t, "02:00:00:00:00:01")
	other := mustMAC(t, "02:00:00:00:00:02")
	for _, m := range []macaddr.MAC{flood, other} {
		if err := db.EnsureMAC(ctx, m.String(), store.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var pairs []Pair
	for i := 1; i <= 50; i++ {
		pairs = append(pairs, Pair{MAC: flood, IP: netip.MustParseAddr(fmt.Sprintf("192.0.2.%d", i))})
	}
	pairs = append(pairs, Pair{MAC: other, IP: netip.MustParseAddr("198.51.100.1")})
	s.ObserveARP(ctx, pairs, "snmp-arp")
	if got, want := len(s.queue), maxNewPairsPerMAC+1; got != want {
		t.Fatalf("queued %d pairs, want %d (cap per MAC plus one for the other MAC)", got, want)
	}
}

func TestObserveDHCPCapsNewPairsPerMAC(t *testing.T) {
	ctx := context.Background()
	s, db := newTestService(t)
	flood := mustMAC(t, "02:00:00:00:00:01")
	if err := db.EnsureMAC(ctx, flood.String(), store.Now()); err != nil {
		t.Fatal(err)
	}
	var leases []DHCPLease
	for i := 1; i <= 30; i++ {
		leases = append(leases, DHCPLease{MAC: flood, IP: netip.MustParseAddr(fmt.Sprintf("192.0.2.%d", i))})
	}
	s.ObserveDHCP(ctx, leases)
	if got := len(s.queue); got != maxNewPairsPerMAC {
		t.Fatalf("queued %d pairs, want %d", got, maxNewPairsPerMAC)
	}
}

func TestReprobeCapsPairsPerMAC(t *testing.T) {
	ctx := context.Background()
	s, db := newTestService(t)
	flood := mustMAC(t, "02:00:00:00:00:01")
	if err := db.EnsureMAC(ctx, flood.String(), store.Now()); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		if _, err := db.RecordIP(ctx, flood.String(), fmt.Sprintf("192.0.2.%d", i), "snmp-arp", store.Now()); err != nil {
			t.Fatal(err)
		}
	}
	s.reprobe(ctx)
	if got := len(s.queue); got != maxReprobePairsPerMAC {
		t.Fatalf("re-probe queued %d pairs, want %d", got, maxReprobePairsPerMAC)
	}
}

func TestPruneLastProbe(t *testing.T) {
	s, _ := newTestService(t)
	now := time.Now()
	old := netip.MustParseAddr("192.0.2.1")
	fresh := netip.MustParseAddr("192.0.2.2")
	s.lastProbe[old] = now.Add(-3 * time.Hour)
	s.lastProbe[fresh] = now.Add(-time.Minute)
	s.pruneLastProbe(now)
	if _, ok := s.lastProbe[old]; ok {
		t.Fatal("old entry was not pruned")
	}
	if _, ok := s.lastProbe[fresh]; !ok {
		t.Fatal("fresh entry was pruned")
	}

	// The size bound drops the oldest entries first.
	s.lastProbe = map[netip.Addr]time.Time{}
	for i := 0; i < lastProbeMaxSize+10; i++ {
		ip := netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)})
		s.lastProbe[ip] = now.Add(-time.Duration(i) * time.Millisecond)
	}
	s.pruneLastProbe(now)
	if len(s.lastProbe) != lastProbeMaxSize {
		t.Fatalf("map has %d entries, want %d", len(s.lastProbe), lastProbeMaxSize)
	}
}

func TestSuggestMergesCapPerName(t *testing.T) {
	ctx := context.Background()
	s, db := newTestService(t)
	if _, err := db.StartEvent(ctx, "lan", ""); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		m := fmt.Sprintf("02:00:00:00:01:%02x", i)
		// Offline MACs, so the concurrency rule does not interfere.
		if err := db.EnsureMAC(ctx, m, store.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.RecordHostname(ctx, m, "same", "netbios", store.Now()); err != nil {
			t.Fatal(err)
		}
	}
	s.suggestMerges(ctx, "02:00:00:00:01:01", "same")
	list, _, err := db.ListMergeSuggestions(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != maxSuggestionsPerName {
		t.Fatalf("%d suggestions, want %d", len(list), maxSuggestionsPerName)
	}
}

func TestEveryRecoversFromPanic(t *testing.T) {
	s, _ := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan struct{}, 8)
	go s.every(ctx, 5*time.Millisecond, func() {
		calls <- struct{}{}
		panic("boom")
	})
	for i := 0; i < 3; i++ {
		select {
		case <-calls:
		case <-time.After(2 * time.Second):
			t.Fatalf("loop stopped after %d iterations", i)
		}
	}
}

func TestSuggestMergesSkipsConcurrentlyOnlineMACs(t *testing.T) {
	ctx := context.Background()
	s, db := newTestService(t)
	if _, err := db.StartEvent(ctx, "lan", ""); err != nil {
		t.Fatal(err)
	}
	long := store.Now().Add(-time.Hour)
	macs := map[string]time.Time{
		"02:00:00:00:02:01": store.Now(), // the MAC that just got the name
		"02:00:00:00:02:02": store.Now(), // online now: concurrent, no suggestion
		"02:00:00:00:02:03": long,        // offline for an hour: plausible adapter switch
	}
	for m, at := range macs {
		if err := db.EnsureMAC(ctx, m, at); err != nil {
			t.Fatal(err)
		}
		if _, err := db.RecordHostname(ctx, m, "same", "netbios", store.Now()); err != nil {
			t.Fatal(err)
		}
	}
	s.suggestMerges(ctx, "02:00:00:00:02:01", "same")
	list, _, err := db.ListMergeSuggestions(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].MACB != "02:00:00:00:02:03" {
		t.Fatalf("suggestions = %+v, want only the one with the offline MAC", list)
	}
}
