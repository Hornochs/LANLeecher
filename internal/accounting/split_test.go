package accounting

import (
	"math"
	"math/rand"
	"testing"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

var (
	m1 = macaddr.MAC{2, 0, 0, 0, 0, 1}
	m2 = macaddr.MAC{2, 0, 0, 0, 0, 2}
	m3 = macaddr.MAC{2, 0, 0, 0, 0, 3}
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name   string
		port   uint64
		agent  map[macaddr.MAC]uint64
		shares map[macaddr.MAC]uint64
		rest   uint64
	}{
		{"agents below port: exact shares, rest to the port", 1000, map[macaddr.MAC]uint64{m1: 300, m2: 200}, map[macaddr.MAC]uint64{m1: 300, m2: 200}, 500},
		{"exactly the port", 500, map[macaddr.MAC]uint64{m1: 300, m2: 200}, map[macaddr.MAC]uint64{m1: 300, m2: 200}, 0},
		{"agents above port: proportional", 100, map[macaddr.MAC]uint64{m1: 300, m2: 100}, map[macaddr.MAC]uint64{m1: 75, m2: 25}, 0},
		{"remainder to largest fraction", 10, map[macaddr.MAC]uint64{m1: 10, m2: 10, m3: 10}, map[macaddr.MAC]uint64{m1: 4, m2: 3, m3: 3}, 0},
		{"zero port delta", 0, map[macaddr.MAC]uint64{m1: 5}, map[macaddr.MAC]uint64{}, 0},
		{"zero agent counters", 50, map[macaddr.MAC]uint64{m1: 0}, map[macaddr.MAC]uint64{}, 50},
		{"no agents", 50, nil, map[macaddr.MAC]uint64{}, 50},
	}
	for _, c := range cases {
		shares, rest := Split(c.port, c.agent)
		if rest != c.rest || len(shares) != len(c.shares) {
			t.Errorf("%s: shares %v rest %d, want %v rest %d", c.name, shares, rest, c.shares, c.rest)
			continue
		}
		for m, v := range c.shares {
			if shares[m] != v {
				t.Errorf("%s: %s got %d, want %d", c.name, m, shares[m], v)
			}
		}
	}
}

// TestSplitInvariant: whatever the agents claim, the shares plus the rest are
// exactly the port delta and no MAC gets more than it reported.
func TestSplitInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		port := rng.Uint64() >> uint(rng.Intn(64))
		agent := map[macaddr.MAC]uint64{}
		for j := 0; j < 1+rng.Intn(5); j++ {
			agent[macaddr.MAC{2, 0, 0, 0, 1, byte(j)}] = rng.Uint64() >> uint(rng.Intn(64))
		}
		if i%100 == 0 {
			agent[m1] = math.MaxUint64 // overflowing sums
		}
		shares, rest := Split(port, agent)
		sum := rest
		for m, v := range shares {
			if v > agent[m] {
				t.Fatalf("case %d: %s got %d, reported only %d", i, m, v, agent[m])
			}
			sum += v
		}
		if sum != port {
			t.Fatalf("case %d: shares+rest = %d, port %d (agent %v)", i, sum, port, agent)
		}
	}
}
