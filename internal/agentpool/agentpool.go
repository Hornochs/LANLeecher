// Package agentpool keeps the counters that host agents report per MAC and
// turns them into deltas for the poller. It lives in memory only: after a
// restart the first report of every interface just sets the baseline again.
//
// Agents send absolute byte counters (already in the MAC's point of view:
// down = traffic to the MAC). The pool computes deltas per (agent, MAC,
// interface), keeps them as pending until the poller books the next delta of
// the switch port that carries the MAC, and drops pending deltas nobody
// took. A MAC reported by two agents at the same time is ignored until one
// of them stops reporting it.
package agentpool

import (
	"sort"
	"sync"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

// Interface is one reported counter pair.
type Interface struct {
	MAC    macaddr.MAC
	IfName string
	Kind   string // host, vm, ct
	Name   string // VM or container name (optional)
	VMID   int
	Down   uint64 // bytes to the MAC
	Up     uint64 // bytes from the MAC
}

// Delta is the traffic of a MAC since the last Take.
type Delta struct {
	Down, Up uint64
}

// Status of a MAC for the agents page.
const (
	StatusWaiting  = "waiting"  // baseline set, no delta yet
	StatusPending  = "pending"  // delta waits for the next port poll
	StatusSplit    = "split"    // last delta was used to split a port
	StatusUnused   = "unused"   // deltas expire: MAC not on a shared port
	StatusConflict = "conflict" // reported by two agents
)

// MACStatus describes one reported MAC of an agent.
type MACStatus struct {
	MAC      macaddr.MAC
	IfName   string
	Kind     string
	Name     string
	VMID     int
	Status   string
	LastSeen time.Time
	// PortID is the port the last delta split (StatusSplit).
	PortID int64
}

// Options tune the pool.
type Options struct {
	// PendingTTL drops deltas no port poll took (default 90 s).
	PendingTTL time.Duration
	// StateTTL forgets counters of interfaces that stopped reporting (stopped
	// VMs), so their next report sets a new baseline (default 10 min).
	StateTTL time.Duration
	// MaxBps is the plausibility limit per interface; larger deltas are
	// dropped (default 100 Gbit/s).
	MaxBps float64
}

type stateKey struct {
	agent  int64
	mac    macaddr.MAC
	ifname string
}

type counter struct {
	down, up uint64
	at       time.Time
}

type pending struct {
	agent       int64
	down, up    uint64
	first, last time.Time
}

type macInfo struct {
	agent    int64
	ifname   string
	kind     string
	name     string
	vmid     int
	lastSeen time.Time
	status   string
	portID   int64
}

// Pool is safe for concurrent use.
type Pool struct {
	opts    Options
	mu      sync.Mutex
	state   map[stateKey]counter
	pending map[macaddr.MAC]*pending
	// owner is the agent that reports a MAC; others reporting it at the same
	// time mark it as conflicting until the owner goes quiet.
	macs     map[macaddr.MAC]*macInfo
	conflict map[macaddr.MAC]time.Time
}

// New creates a pool.
func New(o Options) *Pool {
	if o.PendingTTL <= 0 {
		o.PendingTTL = 90 * time.Second
	}
	if o.StateTTL <= 0 {
		o.StateTTL = 10 * time.Minute
	}
	if o.MaxBps <= 0 {
		o.MaxBps = 100e9
	}
	return &Pool{opts: o, state: map[stateKey]counter{}, pending: map[macaddr.MAC]*pending{},
		macs: map[macaddr.MAC]*macInfo{}, conflict: map[macaddr.MAC]time.Time{}}
}

// ReportResult counts what a report did.
type ReportResult struct {
	Accepted  int // counters stored
	Conflicts int // MACs also reported by another agent
}

// Report stores the counters of one agent report received at now.
func (p *Pool) Report(agent int64, now time.Time, ifs []Interface) ReportResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expire(now)
	var res ReportResult
	for _, in := range ifs {
		if in.MAC.IsZero() || in.MAC.IsMulticast() {
			continue
		}
		info := p.macs[in.MAC]
		if info != nil && info.agent != agent && now.Sub(info.lastSeen) < p.opts.StateTTL/5 {
			// Another agent reports this MAC right now.
			p.conflict[in.MAC] = now
			delete(p.pending, in.MAC)
			res.Conflicts++
			continue
		}
		if info == nil || info.agent != agent {
			info = &macInfo{agent: agent, status: StatusWaiting}
			p.macs[in.MAC] = info
		}
		info.ifname, info.kind, info.name, info.vmid, info.lastSeen = in.IfName, in.Kind, in.Name, in.VMID, now

		key := stateKey{agent, in.MAC, in.IfName}
		prev, ok := p.state[key]
		p.state[key] = counter{down: in.Down, up: in.Up, at: now}
		res.Accepted++
		if !ok {
			continue // baseline
		}
		elapsed := now.Sub(prev.at)
		if elapsed <= 0 {
			continue
		}
		down := delta(prev.down, in.Down)
		up := delta(prev.up, in.Up)
		limit := p.opts.MaxBps / 8 * elapsed.Seconds() * 1.5
		if float64(down) > limit || float64(up) > limit {
			continue // implausible (counter garbage)
		}
		if down == 0 && up == 0 {
			continue
		}
		pd := p.pending[in.MAC]
		if pd == nil || pd.agent != agent {
			pd = &pending{agent: agent, first: now}
			p.pending[in.MAC] = pd
		}
		pd.down += down
		pd.up += up
		pd.last = now
		if info.status != StatusSplit {
			info.status = StatusPending
		}
	}
	return res
}

// delta of a counter; a smaller value means the counter restarted (VM
// restart, migration, interface recreated) and counts from zero.
func delta(prev, cur uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	return cur
}

// Take removes and returns the pending deltas of the given MACs (the MACs a
// switch port carries right now). Conflicting MACs are left out.
func (p *Pool) Take(macs []macaddr.MAC, portID int64, now time.Time) map[macaddr.MAC]Delta {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expire(now)
	var out map[macaddr.MAC]Delta
	for _, m := range macs {
		pd := p.pending[m]
		if pd == nil {
			continue
		}
		if _, bad := p.conflict[m]; bad {
			continue
		}
		delete(p.pending, m)
		if out == nil {
			out = map[macaddr.MAC]Delta{}
		}
		out[m] = Delta{Down: pd.down, Up: pd.up}
		if info := p.macs[m]; info != nil {
			info.status, info.portID = StatusSplit, portID
		}
	}
	return out
}

// expire drops old pending deltas, counters and conflicts. Caller holds mu.
func (p *Pool) expire(now time.Time) {
	for m, pd := range p.pending {
		if now.Sub(pd.last) > p.opts.PendingTTL {
			delete(p.pending, m)
			if info := p.macs[m]; info != nil && info.status != StatusSplit {
				info.status = StatusUnused
			}
		}
	}
	for k, c := range p.state {
		if now.Sub(c.at) > p.opts.StateTTL {
			delete(p.state, k)
		}
	}
	for m, info := range p.macs {
		if now.Sub(info.lastSeen) > p.opts.StateTTL {
			delete(p.macs, m)
		}
	}
	for m, at := range p.conflict {
		if now.Sub(at) > p.opts.StateTTL/5 {
			delete(p.conflict, m)
		}
	}
}

// Status lists the MACs an agent reports, sorted by MAC.
func (p *Pool) Status(agent int64, now time.Time) []MACStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expire(now)
	var out []MACStatus
	for m, info := range p.macs {
		if info.agent != agent {
			continue
		}
		st := info.status
		if _, bad := p.conflict[m]; bad {
			st = StatusConflict
		}
		out = append(out, MACStatus{MAC: m, IfName: info.ifname, Kind: info.kind, Name: info.name, VMID: info.vmid,
			Status: st, LastSeen: info.lastSeen, PortID: info.portID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC.String() < out[j].MAC.String() })
	return out
}

// Forget drops everything of an agent (token revoked).
func (p *Pool) Forget(agent int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.state {
		if k.agent == agent {
			delete(p.state, k)
		}
	}
	for m, pd := range p.pending {
		if pd.agent == agent {
			delete(p.pending, m)
		}
	}
	for m, info := range p.macs {
		if info.agent == agent {
			delete(p.macs, m)
		}
	}
}
