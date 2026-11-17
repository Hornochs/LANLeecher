package scheduler

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/agentpool"
	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

// TestBookSplitsSharedPortWithAgents: a hypervisor port with three MACs;
// the agent reports two guests. They get their share, the rest stays with
// the port group, and all subjects together equal the port delta.
func TestBookSplitsSharedPortWithAgents(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "s.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, log); err != nil {
		t.Fatal(err)
	}
	sw := &store.Switch{Name: "sw", Host: "192.0.2.1", Port: 161, SNMPVersion: "v2c", CredentialsEnc: []byte{0}, FDBAdapter: "auto", Enabled: true}
	if err := db.CreateSwitch(ctx, sw); err != nil {
		t.Fatal(err)
	}
	ev, err := db.StartEvent(ctx, "LAN", "")
	if err != nil {
		t.Fatal(err)
	}
	host := macaddr.MAC{2, 0, 0, 0, 0, 1}
	vm1 := macaddr.MAC{0xbc, 0x24, 0x11, 0, 0, 1}
	vm2 := macaddr.MAC{0xbc, 0x24, 0x11, 0, 0, 2}
	t0 := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

	for _, m := range []macaddr.MAC{host, vm1, vm2} { // the FDB poll knows them
		if err := db.EnsureMAC(ctx, m.String(), t0); err != nil {
			t.Fatal(err)
		}
	}
	pool := agentpool.New(agentpool.Options{})
	pool.Report(9, t0, []agentpool.Interface{{MAC: vm1, IfName: "tap101i0"}, {MAC: vm2, IfName: "tap102i0"}})
	pool.Report(9, t0.Add(20*time.Second), []agentpool.Interface{
		{MAC: vm1, IfName: "tap101i0", Down: 6000, Up: 600},
		{MAC: vm2, IfName: "tap102i0", Down: 2000, Up: 200},
	})
	p := &poller{m: &Manager{db: db, log: log, Agents: pool}, sw: sw, log: log}

	at := t0.Add(30 * time.Second)
	err = db.InTx(ctx, func(tx *store.Tx) error {
		ports, err := tx.UpsertPorts(ctx, sw.ID, []store.PortObservation{
			{IfIndex: 5, IfName: "Gi5", OperUp: true, HCSupported: true, Physical: true, DefaultRole: "client"},
		}, at)
		if err != nil {
			return err
		}
		return p.book(ctx, tx, ev.ID, ports[5], []macaddr.MAC{host, vm1, vm2}, 10000, 1000, at)
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := db.Export(ctx, store.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]int64{}
	for _, tr := range doc.Events[0].Totals {
		key := tr.Type + ":" + tr.Subject
		if tr.Type == store.SubjectPortGroup {
			key = tr.Type
		}
		v := got[key]
		got[key] = [2]int64{v[0] + tr.Down, v[1] + tr.Up}
	}
	want := map[string][2]int64{
		"mac:" + vm1.String(): {6000, 600},
		"mac:" + vm2.String(): {2000, 200},
		"port_group":          {2000, 200}, // the host itself and anything else
	}
	if len(got) != len(want) {
		t.Fatalf("subjects %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %v, want %v", k, got[k], v)
		}
	}

	// The next poll has no new agent data: everything goes to the port group again.
	err = db.InTx(ctx, func(tx *store.Tx) error {
		port, err := db.ListPorts(ctx, sw.ID)
		if err != nil {
			return err
		}
		return p.book(ctx, tx, ev.ID, port[0], []macaddr.MAC{host, vm1, vm2}, 500, 50, at.Add(30*time.Second))
	})
	if err != nil {
		t.Fatal(err)
	}
	if st := pool.Status(9, at); len(st) != 2 || st[0].Status != agentpool.StatusSplit {
		t.Fatalf("status: %+v", st)
	}
}
