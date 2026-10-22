package resolver

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/lan-dot-party/lanleecher/internal/config"
	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

func TestObserveDHCP(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := db.Migrate(ctx, log); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Resolver
	db.HostnamePriority = cfg.HostnamePriority
	cfg.Order = nil // no probes in this test
	s := New(db, cfg, log)

	known, _ := macaddr.Parse("02:00:00:00:00:01")
	unknown, _ := macaddr.Parse("02:00:00:00:00:02")
	if err := db.EnsureMAC(ctx, known.String(), store.Now()); err != nil {
		t.Fatal(err)
	}
	// A NetBIOS name exists already; the DHCP hostname ranks higher.
	if _, err := db.RecordHostname(ctx, known.String(), "OLD-NETBIOS", "netbios", store.Now()); err != nil {
		t.Fatal(err)
	}

	n := s.ObserveDHCP(ctx, []DHCPLease{
		{MAC: known, IP: netip.MustParseAddr("192.0.2.10"), Hostname: "gamer-pc.lan.example."},
		{MAC: unknown, IP: netip.MustParseAddr("192.0.2.11"), Hostname: "stranger"},
	})
	if n != 1 {
		t.Fatalf("ObserveDHCP counted %d known leases, want 1", n)
	}
	infos, err := db.MACInfos(ctx, []string{known.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got := infos[known.String()]; got.Hostname != "gamer-pc.lan.example" || got.IP != "192.0.2.10" {
		t.Fatalf("MAC info = %+v, want the DHCP hostname and IP", got)
	}
	if ok, _ := db.MACKnown(ctx, unknown.String()); ok {
		t.Fatal("a MAC only seen in DHCP must not be created")
	}
}
