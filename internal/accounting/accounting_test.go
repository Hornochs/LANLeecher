package accounting

import (
	"math"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

var t0 = time.Date(2026, 11, 13, 16, 0, 0, 0, time.UTC)

// reading builds a sample 'sec' seconds after t0 with sysUpTime in seconds.
func reading(value uint64, uptimeSec uint32, disc uint32, sec int) Reading {
	return Reading{Value: value, SysUptime: uptimeSec * 100, Discontinuity: disc, At: t0.Add(time.Duration(sec) * time.Second)}
}

func ptr(r Reading) *Reading { return &r }

func TestDelta(t *testing.T) {
	const gbit = 1000 // ifHighSpeed in Mbit/s
	// 1 Gbit/s for 30 s = 3.75e9 octets.
	full30s := uint64(1e9 / 8 * 30)

	tests := []struct {
		name  string
		prev  *Reading
		cur   Reading
		speed uint64
		want  uint64
		kind  Kind
	}{
		{
			name: "first reading credits nothing",
			prev: nil,
			cur:  reading(5_000_000, 1000, 0, 0),
			want: 0, kind: KindFirst,
		},
		{
			name: "normal increase",
			prev: ptr(reading(1_000, 1000, 0, 0)),
			cur:  reading(1_000_000, 1030, 0, 30),
			want: 999_000, kind: KindNormal, speed: gbit,
		},
		{
			name: "no traffic",
			prev: ptr(reading(42, 1000, 0, 0)),
			cur:  reading(42, 1030, 0, 30),
			want: 0, kind: KindNormal, speed: gbit,
		},
		{
			name: "64-bit wrap",
			prev: ptr(reading(math.MaxUint64-99, 1000, 0, 0)),
			cur:  reading(900, 1030, 0, 30),
			want: 1000, kind: KindWrap, speed: gbit,
		},
		{
			name: "switch reboot credits value since reboot",
			prev: ptr(reading(9_000_000_000, 500_000, 0, 0)),
			cur:  reading(2_000_000, 20, 0, 30),
			want: 2_000_000, kind: KindReset, speed: gbit,
		},
		{
			name: "counter reset via ifCounterDiscontinuityTime",
			prev: ptr(reading(9_000_000_000, 5000, 0, 0)),
			cur:  reading(3_000_000, 5030, 5020*100, 30),
			want: 3_000_000, kind: KindReset, speed: gbit,
		},
		{
			name: "reset plausibility uses time since reboot",
			// 10 s since reboot at 1 Gbit/s allows ~1.375e9; 5e9 is too much.
			prev: ptr(reading(9_000_000_000, 500_000, 0, 0)),
			cur:  reading(5_000_000_000, 10, 0, 30),
			want: 0, kind: KindImplausible, speed: gbit,
		},
		{
			name: "line rate is plausible",
			prev: ptr(reading(0, 1000, 0, 0)),
			cur:  reading(full30s, 1030, 0, 30),
			want: full30s, kind: KindNormal, speed: gbit,
		},
		{
			name: "fantasy value above line rate is dropped",
			prev: ptr(reading(0, 1000, 0, 0)),
			cur:  reading(full30s*2, 1030, 0, 30),
			want: 0, kind: KindImplausible, speed: gbit,
		},
		{
			name: "fake wrap after silent reset is caught by plausibility",
			prev: ptr(reading(5_000, 1000, 0, 0)),
			cur:  reading(10, 1030, 0, 30),
			want: 0, kind: KindImplausible, speed: gbit,
		},
		{
			name: "gap after failed polls uses the longer elapsed time",
			// 10 minutes at 100 Mbit/s: 7.5e9 octets fit.
			prev: ptr(reading(0, 1000, 0, 0)),
			cur:  reading(7_000_000_000, 1600, 0, 600),
			want: 7_000_000_000, kind: KindNormal, speed: 100,
		},
		{
			name: "unknown speed disables the check",
			prev: ptr(reading(0, 1000, 0, 0)),
			cur:  reading(1<<50, 1030, 0, 30),
			want: 1 << 50, kind: KindNormal, speed: 0,
		},
		{
			name: "slow switch clock does not cause a false rejection",
			// sysUpTime only advanced 10 s, but 30 s passed on the wall clock.
			prev: ptr(reading(0, 1000, 0, 0)),
			cur:  reading(full30s, 1010, 0, 30),
			want: full30s, kind: KindNormal, speed: gbit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Delta(tt.prev, tt.cur, tt.speed)
			if got.Delta != tt.want || got.Kind != tt.kind {
				t.Fatalf("Delta() = %d (%s), want %d (%s)", got.Delta, got.Kind, tt.want, tt.kind)
			}
		})
	}
}

func TestResetNeverReducesTotal(t *testing.T) {
	// Sum deltas across a reboot; the total must only grow.
	readings := []Reading{
		reading(0, 100, 0, 0),
		reading(1_000_000, 130, 0, 30),
		reading(2_000_000, 160, 0, 60),
		reading(500_000, 30, 0, 90), // reboot
		reading(900_000, 60, 0, 120),
	}
	var total uint64
	var prev *Reading
	for _, r := range readings {
		res := Delta(prev, r, 1000)
		if res.Delta > math.MaxUint64-total {
			t.Fatal("overflow")
		}
		total += res.Delta
		prev = ptr(r)
	}
	if want := uint64(2_000_000 + 900_000); total != want {
		t.Fatalf("total = %d, want %d", total, want)
	}
}

func TestClientDirection(t *testing.T) {
	// ifHCInOctets (into the switch) is the client's upload.
	down, up := ClientDirection(100, 900)
	if down != 900 || up != 100 {
		t.Fatalf("ClientDirection(in=100, out=900) = down %d, up %d; want down 900, up 100", down, up)
	}
}

func TestAttribute(t *testing.T) {
	a := mustMAC(t, "aa:aa:aa:aa:aa:01")
	b := mustMAC(t, "aa:aa:aa:aa:aa:02")
	mcast := mustMAC(t, "01:00:5e:00:00:fb")

	tests := []struct {
		name    string
		role    Role
		macs    []macaddr.MAC
		kind    TargetKind
		mac     macaddr.MAC
		members int
	}{
		{"client with one MAC", RoleClient, []macaddr.MAC{a}, TargetMAC, a, 0},
		{"client with duplicate entries", RoleClient, []macaddr.MAC{a, a}, TargetMAC, a, 0},
		{"client with mini switch", RoleClient, []macaddr.MAC{a, b}, TargetPortGroup, macaddr.MAC{}, 2},
		{"multi-client with one MAC is still a group", RoleMultiClient, []macaddr.MAC{a}, TargetPortGroup, macaddr.MAC{}, 1},
		{"server like client", RoleServer, []macaddr.MAC{b}, TargetMAC, b, 0},
		{"uplink is never attributed", RoleUplink, []macaddr.MAC{a, b}, TargetNone, macaddr.MAC{}, 0},
		{"no MAC learned", RoleClient, nil, TargetUnassigned, macaddr.MAC{}, 0},
		{"multi-client without MAC", RoleMultiClient, nil, TargetUnassigned, macaddr.MAC{}, 0},
		{"multicast is ignored", RoleClient, []macaddr.MAC{mcast, a}, TargetMAC, a, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Attribute(tt.role, tt.macs)
			if got.Kind != tt.kind || got.MAC != tt.mac || len(got.Members) != tt.members {
				t.Fatalf("Attribute() = %+v, want kind %s mac %s members %d", got, tt.kind, tt.mac, tt.members)
			}
		})
	}
}

func TestRoleDefaults(t *testing.T) {
	for role, hidden := range map[Role]bool{RoleClient: false, RoleMultiClient: false, RoleServer: true, RoleUplink: true} {
		if role.HiddenByDefault() != hidden {
			t.Errorf("%s.HiddenByDefault() = %v, want %v", role, !hidden, hidden)
		}
	}
	if Role("trunk").Valid() {
		t.Error("unknown role reported as valid")
	}
}

func mustMAC(t *testing.T, s string) macaddr.MAC {
	t.Helper()
	m, err := macaddr.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
