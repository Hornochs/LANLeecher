// Package accounting turns raw SNMP counter readings into traffic deltas and
// decides who gets credited for them.
//
// Everything here is pure: no I/O, no clocks, no database. The poller feeds
// readings in and persists what comes out. A MAC's total is only ever the sum
// of its deltas, so a counter reset on a switch never resets a total.
package accounting

import (
	"time"
)

// Reading is one sample of a 64-bit interface octet counter together with the
// switch-side timing information needed to detect resets.
type Reading struct {
	// Value is the raw ifHCInOctets or ifHCOutOctets value.
	Value uint64
	// SysUptime is sysUpTime in TimeTicks (1/100 s).
	SysUptime uint32
	// Discontinuity is ifCounterDiscontinuityTime in TimeTicks; 0 if unsupported.
	Discontinuity uint32
	// At is the wall-clock time of the poll.
	At time.Time
}

// Kind classifies how a delta was computed.
type Kind int

const (
	// KindFirst means there was no previous reading; the delta is 0.
	KindFirst Kind = iota
	// KindNormal is new - old.
	KindNormal
	// KindWrap is a 64-bit counter wrap: new + 2^64 - old.
	KindWrap
	// KindReset means the switch rebooted or reset the counter; the new value
	// is the traffic since the reset and is credited in full.
	KindReset
	// KindImplausible means the delta exceeded the link speed and was dropped.
	KindImplausible
)

func (k Kind) String() string {
	switch k {
	case KindFirst:
		return "first"
	case KindNormal:
		return "normal"
	case KindWrap:
		return "wrap"
	case KindReset:
		return "reset"
	case KindImplausible:
		return "implausible"
	}
	return "unknown"
}

// Result of a delta computation.
type Result struct {
	// Delta is the number of octets to credit (0 for First and Implausible).
	Delta uint64
	Kind  Kind
	// Elapsed is the time span the delta covers, used for the plausibility
	// check and live throughput.
	Elapsed time.Duration
	// Raw is the delta before the plausibility check (for logging).
	Raw uint64
}

// PlausibilityFactor allows 10 % above line rate for measurement jitter.
const PlausibilityFactor = 1.1

// minElapsed avoids rejecting deltas when two polls are very close together.
const minElapsed = time.Second

const tick = 10 * time.Millisecond

// Delta computes the octets transferred between prev and cur.
//
// speedMbps is ifHighSpeed; 0 disables the plausibility check.
// prev is nil for the first reading of a port.
//
// Reset detection:
//   - sysUpTime went backwards: the switch rebooted.
//   - ifCounterDiscontinuityTime changed: the counter was reset.
//
// In both cases the new value is the traffic since the reset. The plausibility
// check then uses the time since the reset (per sysUpTime) instead of the poll
// interval. Without a reset signal a smaller value is a 64-bit wrap.
func Delta(prev *Reading, cur Reading, speedMbps uint64) Result {
	if prev == nil {
		return Result{Kind: KindFirst}
	}

	var res Result
	reset := cur.SysUptime < prev.SysUptime || cur.Discontinuity != prev.Discontinuity
	switch {
	case reset:
		res.Kind = KindReset
		res.Raw = cur.Value
		since := cur.SysUptime
		if cur.Discontinuity > 0 && cur.Discontinuity <= cur.SysUptime {
			since = cur.SysUptime - cur.Discontinuity
		}
		res.Elapsed = time.Duration(since) * tick
	case cur.Value < prev.Value:
		res.Kind = KindWrap
		// Unsigned subtraction wraps modulo 2^64: new + 2^64 - old.
		res.Raw = cur.Value - prev.Value
		res.Elapsed = elapsed(prev, cur)
	default:
		res.Kind = KindNormal
		res.Raw = cur.Value - prev.Value
		res.Elapsed = elapsed(prev, cur)
	}

	if !Plausible(res.Raw, res.Elapsed, speedMbps) {
		res.Kind = KindImplausible
		return res
	}
	res.Delta = res.Raw
	return res
}

// elapsed returns the larger of switch-uptime difference and wall-clock
// difference, so neither a slow switch clock nor a delayed poll causes a
// false plausibility rejection.
func elapsed(prev *Reading, cur Reading) time.Duration {
	var up time.Duration
	if cur.SysUptime > prev.SysUptime {
		up = time.Duration(cur.SysUptime-prev.SysUptime) * tick
	}
	wall := cur.At.Sub(prev.At)
	if wall > up {
		return wall
	}
	return up
}

// Plausible reports whether octets over elapsed fit the link speed.
func Plausible(octets uint64, elapsed time.Duration, speedMbps uint64) bool {
	if speedMbps == 0 || octets == 0 {
		return true
	}
	if elapsed < minElapsed {
		elapsed = minElapsed
	}
	maxOctets := float64(speedMbps) * 1e6 / 8 * elapsed.Seconds() * PlausibilityFactor
	return float64(octets) <= maxOctets
}

// ClientDirection maps port counters to the client's point of view.
//
// The counters are seen from the switch port: ifHCInOctets is what the client
// sent into the switch (its upload), ifHCOutOctets is what the switch sent to
// the client (its download). This is the only place where that mapping lives.
func ClientDirection(portIn, portOut uint64) (down, up uint64) {
	return portOut, portIn
}
