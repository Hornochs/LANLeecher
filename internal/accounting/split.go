package accounting

import (
	"math/bits"
	"sort"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

// Split divides one direction of a port delta between MACs whose host agent
// reported its own counters for the same period (ADR: the switch stays
// authoritative, agent counters only split a shared port).
//
//   - If the agent counters together are at most the port delta, every MAC
//     gets exactly its agent counter and the rest stays with the port
//     (traffic of MACs without an agent).
//   - If they are more (traffic between guests of one host never reaches
//     the switch port, or the periods are skewed), the port delta is split
//     in proportion to the agent counters and nothing is left.
//
// The sum of all shares plus rest always equals portDelta, so an agent can
// only shift traffic within its port, never add any. Shares are integers;
// the remainder of the proportional split goes to the largest fractions
// (ties by MAC order), so the result is deterministic.
func Split(portDelta uint64, agent map[macaddr.MAC]uint64) (shares map[macaddr.MAC]uint64, rest uint64) {
	shares = make(map[macaddr.MAC]uint64, len(agent))
	var sum uint64
	overflow := false
	for _, v := range agent {
		var carry uint64
		sum, carry = bits.Add64(sum, v, 0)
		if carry != 0 {
			overflow = true
		}
	}
	if !overflow && sum <= portDelta {
		for m, v := range agent {
			if v > 0 {
				shares[m] = v
			}
		}
		return shares, portDelta - sum
	}
	if overflow {
		// Implausible input; scale every counter down so the sum fits.
		scaled := make(map[macaddr.MAC]uint64, len(agent))
		sum = 0
		for m, v := range agent {
			scaled[m] = v >> 8
			sum += scaled[m]
		}
		agent = scaled
		if sum == 0 {
			return shares, portDelta
		}
	}
	type frac struct {
		mac macaddr.MAC
		rem uint64 // remainder of portDelta*v / sum, compared over the same denominator
	}
	fracs := make([]frac, 0, len(agent))
	var given uint64
	for m, v := range agent {
		if v == 0 {
			continue
		}
		hi, lo := bits.Mul64(portDelta, v)
		q, r := bits.Div64(hi, lo, sum) // hi < sum because v <= sum and portDelta < sum
		if q > 0 {
			shares[m] = q
		}
		given += q
		fracs = append(fracs, frac{m, r})
	}
	sort.Slice(fracs, func(i, j int) bool {
		if fracs[i].rem != fracs[j].rem {
			return fracs[i].rem > fracs[j].rem
		}
		return fracs[i].mac.String() < fracs[j].mac.String()
	})
	for i := 0; given < portDelta && i < len(fracs); i++ {
		shares[fracs[i].mac]++
		given++
	}
	return shares, portDelta - given
}
