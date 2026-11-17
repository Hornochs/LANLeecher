package agentpool

import (
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

var (
	vm1 = macaddr.MAC{0xbc, 0x24, 0x11, 0, 0, 1}
	vm2 = macaddr.MAC{0xbc, 0x24, 0x11, 0, 0, 2}
	t0  = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
)

func TestDeltasAndTake(t *testing.T) {
	p := New(Options{})
	p.Report(1, t0, []Interface{{MAC: vm1, IfName: "tap101i0", Down: 1000, Up: 100}})
	if got := p.Take([]macaddr.MAC{vm1}, 7, t0); got != nil {
		t.Fatalf("baseline must not give a delta: %v", got)
	}
	p.Report(1, t0.Add(15*time.Second), []Interface{{MAC: vm1, IfName: "tap101i0", Down: 3000, Up: 300}})
	p.Report(1, t0.Add(30*time.Second), []Interface{{MAC: vm1, IfName: "tap101i0", Down: 4000, Up: 400}})
	got := p.Take([]macaddr.MAC{vm1, vm2}, 7, t0.Add(31*time.Second))
	if got[vm1] != (Delta{Down: 3000, Up: 300}) || len(got) != 1 {
		t.Fatalf("take: %v", got)
	}
	if again := p.Take([]macaddr.MAC{vm1}, 7, t0.Add(32*time.Second)); again != nil {
		t.Fatalf("taken twice: %v", again)
	}
	st := p.Status(1, t0.Add(33*time.Second))
	if len(st) != 1 || st[0].Status != StatusSplit || st[0].PortID != 7 {
		t.Fatalf("status: %+v", st)
	}
}

func TestCounterRestartCountsFromZero(t *testing.T) {
	p := New(Options{})
	p.Report(1, t0, []Interface{{MAC: vm1, IfName: "tap101i0", Down: 5000}})
	// VM migrated or restarted: new tap device, counter starts again.
	p.Report(1, t0.Add(15*time.Second), []Interface{{MAC: vm1, IfName: "tap101i0", Down: 200}})
	if got := p.Take([]macaddr.MAC{vm1}, 1, t0.Add(16*time.Second)); got[vm1].Down != 200 {
		t.Fatalf("restart: %v", got)
	}
}

func TestImplausibleDropped(t *testing.T) {
	p := New(Options{MaxBps: 1e9})
	p.Report(1, t0, []Interface{{MAC: vm1, IfName: "eth0", Down: 0}})
	p.Report(1, t0.Add(10*time.Second), []Interface{{MAC: vm1, IfName: "eth0", Down: 10e9}}) // 8 Gbit/s on a 1 Gbit/s limit
	if got := p.Take([]macaddr.MAC{vm1}, 1, t0.Add(11*time.Second)); got != nil {
		t.Fatalf("implausible delta kept: %v", got)
	}
}

func TestPendingExpires(t *testing.T) {
	p := New(Options{PendingTTL: time.Minute})
	p.Report(1, t0, []Interface{{MAC: vm1, IfName: "eth0", Down: 0}})
	p.Report(1, t0.Add(10*time.Second), []Interface{{MAC: vm1, IfName: "eth0", Down: 100}})
	if got := p.Take([]macaddr.MAC{vm1}, 1, t0.Add(2*time.Minute)); got != nil {
		t.Fatalf("expired delta taken: %v", got)
	}
	if st := p.Status(1, t0.Add(2*time.Minute)); len(st) != 1 || st[0].Status != StatusUnused {
		t.Fatalf("status: %+v", st)
	}
}

func TestConflictingAgents(t *testing.T) {
	p := New(Options{})
	p.Report(1, t0, []Interface{{MAC: vm1, IfName: "eth0", Down: 0}})
	p.Report(1, t0.Add(10*time.Second), []Interface{{MAC: vm1, IfName: "eth0", Down: 100}})
	res := p.Report(2, t0.Add(11*time.Second), []Interface{{MAC: vm1, IfName: "eth0", Down: 999999}})
	if res.Conflicts != 1 || res.Accepted != 0 {
		t.Fatalf("second agent: %+v", res)
	}
	if got := p.Take([]macaddr.MAC{vm1}, 1, t0.Add(12*time.Second)); got != nil {
		t.Fatalf("conflicting MAC used: %v", got)
	}
	if st := p.Status(1, t0.Add(12*time.Second)); st[0].Status != StatusConflict {
		t.Fatalf("status: %+v", st)
	}
	// Agent 1 goes quiet; after a while agent 2 may report the MAC (VM moved).
	later := t0.Add(5 * time.Minute)
	p.Report(2, later, []Interface{{MAC: vm1, IfName: "eth0", Down: 10}})
	p.Report(2, later.Add(10*time.Second), []Interface{{MAC: vm1, IfName: "eth0", Down: 60}})
	if got := p.Take([]macaddr.MAC{vm1}, 1, later.Add(11*time.Second)); got[vm1].Down != 50 {
		t.Fatalf("after hand-over: %v", got)
	}
}

func TestForget(t *testing.T) {
	p := New(Options{})
	p.Report(1, t0, []Interface{{MAC: vm1, IfName: "eth0"}})
	p.Forget(1)
	if st := p.Status(1, t0); len(st) != 0 {
		t.Fatalf("forgotten agent still listed: %+v", st)
	}
}
