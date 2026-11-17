// Package topology computes the links between switches from their
// snapshots. It is made of pure functions: no database, no I/O.
//
// Devices are identified by their MAC addresses (System.ChassisID and
// System.BaseMAC) and, as a fallback, by name (the device's name and its
// sysName, case-insensitive). If a MAC or name belongs to several devices,
// the one with the lowest ID wins.
//
// LLDP links. Every port with a neighbour whose chassis ID is a MAC of a
// known device (else whose sysName is a name of one) gives a link. The far
// port is the neighbour's port whose Name, Descr or Alias equals the
// neighbour's PortID (then PortDesc); if there is none, the far device's port
// that sees this device as its neighbour; if there is none either, the far
// ifIndex is 0 (unknown). A port that is a LAG member is replaced by the LAG
// interface (Port.LAG) on both sides, so a LAG gives one link. A link seen
// from both sides is one link; A is the end with the lower device ID.
//
// FDB links. Only for pairs of devices without an LLDP link. For every
// device A and every other device X whose MAC is learned on A, the port of A
// is the one that carries X's MAC; if X is learned on several ports, the one
// with the fewest learned MACs, then the lowest ifIndex. Several devices can
// share one port of A (a switch further away is also learned on the port
// towards the switch in between), so a device X is taken as directly
// connected to A on port P unless
//   - another device on P has an LLDP link to A on P (X is behind it), or
//   - on X, the port where A is learned also carries another device that is
//     learned on P of A (that device lies between A and X).
//
// The far end is the port of X where A is learned (chosen the same way), or
// 0 when X has no table. The link source is "fdb". A device with a single
// foreign switch on a port is always linked; an unmanaged switch in between
// does not matter. Only MACs of ChassisID and BaseMAC count; per-port MACs of
// a switch are not known.
//
// Self references and neighbours that are not among the devices are ignored.
// The result is sorted by A device, A ifIndex, B device, B ifIndex, source.
package topology

import (
	"sort"
	"strings"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

// Link sources.
const (
	SourceLLDP = "lldp"
	SourceFDB  = "fdb"
)

// Snapshot is what a switch knows about itself and its neighbours.
type Snapshot struct {
	System System
	Ports  []*Port
	FDB    []FDBEntry
}

// System identifies a switch.
type System struct {
	Name      string // sysName
	ChassisID string // LLDP chassis ID of the switch (a MAC for most)
	BaseMAC   string // bridge base MAC address
}

// Port is a port or LAG interface of a switch.
type Port struct {
	IfIndex int
	Name    string
	Descr   string
	Alias   string
	// LAG is the ifIndex of the LAG interface this port is a member of (0 = none).
	LAG     int
	LAGName string
	// Neighbor is the LLDP neighbour on the port (nil = none).
	Neighbor *Neighbor
}

// Neighbor is a system seen by LLDP.
type Neighbor struct {
	SysName   string
	PortID    string
	PortDesc  string
	ChassisID string
}

// FDBEntry is a MAC address learned on an interface.
type FDBEntry struct {
	MAC     string
	IfIndex int
}

// Port returns the port with an ifIndex, or nil.
func (s *Snapshot) Port(ifIndex int) *Port {
	if s == nil {
		return nil
	}
	for _, p := range s.Ports {
		if p.IfIndex == ifIndex {
			return p
		}
	}
	return nil
}

// Device is one switch with its latest snapshot (may be nil).
type Device struct {
	ID   int64
	Name string
	Snap *Snapshot
}

// End is one end of a link. IfIndex is 0 when the far port is unknown; for
// ports in a LAG it is the ifIndex of the LAG interface.
type End struct {
	DeviceID int64
	IfIndex  int
	// Port is the port name for display (may be empty).
	Port string
}

// Link connects two devices. A has the lower device ID.
type Link struct {
	A, B   End
	Source string // "lldp" or "fdb"
}

type node struct {
	dev  Device
	macs map[string]bool
}

type graph struct {
	nodes  []*node // sorted by ID, without duplicate IDs
	byMAC  map[string]*node
	byName map[string]*node
}

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func newGraph(devs []Device) *graph {
	sorted := append([]Device(nil), devs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	g := &graph{byMAC: map[string]*node{}, byName: map[string]*node{}}
	seen := map[int64]bool{}
	for _, d := range sorted {
		if seen[d.ID] {
			continue
		}
		seen[d.ID] = true
		n := &node{dev: d, macs: map[string]bool{}}
		g.nodes = append(g.nodes, n)
		names := []string{d.Name}
		if d.Snap != nil {
			names = append(names, d.Snap.System.Name)
			for _, m := range []string{d.Snap.System.ChassisID, d.Snap.System.BaseMAC} {
				if m = macaddr.NormalizeLoose(m); m != "" {
					n.macs[m] = true
					if _, dup := g.byMAC[m]; !dup {
						g.byMAC[m] = n
					}
				}
			}
		}
		for _, name := range names {
			if name = lower(name); name != "" {
				if _, dup := g.byName[name]; !dup {
					g.byName[name] = n
				}
			}
		}
	}
	return g
}

// resolve finds the device a neighbour is.
func (g *graph) resolve(nb *Neighbor) *node {
	if m := macaddr.NormalizeLoose(nb.ChassisID); m != "" {
		if n := g.byMAC[m]; n != nil {
			return n
		}
	}
	if name := lower(nb.SysName); name != "" {
		return g.byName[name]
	}
	return nil
}

// portEnd describes a port as a link end; member ports of a LAG become the
// LAG interface.
func portEnd(n *node, p *Port) End {
	e := End{DeviceID: n.dev.ID, IfIndex: p.IfIndex, Port: portName(p)}
	if p.LAG != 0 {
		e.IfIndex = p.LAG
		e.Port = p.LAGName
		if lag := n.dev.Snap.Port(p.LAG); lag != nil {
			e.Port = portName(lag)
		}
	}
	return e
}

func portName(p *Port) string {
	if p.Name != "" {
		return p.Name
	}
	return p.Descr
}

// lagEnd is the end for an ifIndex of the FDB, which may be a LAG member.
func lagEnd(n *node, ifIndex int) End {
	if p := n.dev.Snap.Port(ifIndex); p != nil {
		return portEnd(n, p)
	}
	return End{DeviceID: n.dev.ID, IfIndex: ifIndex}
}

// namedPort finds the port of n that a neighbour calls portID or portDesc.
func namedPort(n *node, nb *Neighbor) *Port {
	if n.dev.Snap == nil {
		return nil
	}
	for _, target := range []string{nb.PortID, nb.PortDesc} {
		target = lower(target)
		if target == "" {
			continue
		}
		for _, field := range []func(*Port) string{
			func(p *Port) string { return p.Name },
			func(p *Port) string { return p.Descr },
			func(p *Port) string { return p.Alias },
		} {
			var best *Port
			for _, p := range n.dev.Snap.Ports {
				if lower(field(p)) == target && (best == nil || p.IfIndex < best.IfIndex) {
					best = p
				}
			}
			if best != nil {
				return best
			}
		}
	}
	return nil
}

// seenBy finds the port of far whose neighbour is near (the lowest ifIndex
// when there are several).
func (g *graph) seenBy(far, near *node) *Port {
	if far.dev.Snap == nil {
		return nil
	}
	var best *Port
	for _, p := range far.dev.Snap.Ports {
		if p.Neighbor != nil && g.resolve(p.Neighbor) == near && (best == nil || p.IfIndex < best.IfIndex) {
			best = p
		}
	}
	return best
}

// Build computes the links between the devices.
func Build(devs []Device) []Link {
	g := newGraph(devs)
	links := g.lldpLinks()
	paired := map[[2]int64]bool{}
	for _, l := range links {
		paired[[2]int64{l.A.DeviceID, l.B.DeviceID}] = true
	}
	links = append(links, g.fdbLinks(links, paired)...)
	links = dedupe(links)
	sort.Slice(links, func(i, j int) bool {
		a, b := links[i], links[j]
		switch {
		case a.A.DeviceID != b.A.DeviceID:
			return a.A.DeviceID < b.A.DeviceID
		case a.A.IfIndex != b.A.IfIndex:
			return a.A.IfIndex < b.A.IfIndex
		case a.B.DeviceID != b.B.DeviceID:
			return a.B.DeviceID < b.B.DeviceID
		case a.B.IfIndex != b.B.IfIndex:
			return a.B.IfIndex < b.B.IfIndex
		}
		return a.Source < b.Source
	})
	return links
}

func orient(a, b End, source string) Link {
	if a.DeviceID > b.DeviceID {
		a, b = b, a
	}
	return Link{A: a, B: b, Source: source}
}

func (g *graph) lldpLinks() []Link {
	var out []Link
	for _, n := range g.nodes {
		if n.dev.Snap == nil {
			continue
		}
		for _, p := range n.dev.Snap.Ports {
			if p.Neighbor == nil {
				continue
			}
			far := g.resolve(p.Neighbor)
			if far == nil || far == n {
				continue
			}
			near := portEnd(n, p)
			farEnd := End{DeviceID: far.dev.ID}
			fp := namedPort(far, p.Neighbor)
			if fp == nil {
				fp = g.seenBy(far, n)
			}
			if fp != nil {
				farEnd = portEnd(far, fp)
			}
			out = append(out, orient(near, farEnd, SourceLLDP))
		}
	}
	return out
}

// dedupe removes identical links and links with an unknown far port that
// another link between the same ports already covers.
func dedupe(links []Link) []Link {
	type key struct {
		a, b   int64
		ai, bi int
		src    string
	}
	seen := map[key]bool{}
	var uniq []Link
	for _, l := range links {
		k := key{l.A.DeviceID, l.B.DeviceID, l.A.IfIndex, l.B.IfIndex, l.Source}
		if !seen[k] {
			seen[k] = true
			uniq = append(uniq, l)
		}
	}
	var out []Link
	for _, l := range uniq {
		covered := false
		if l.A.IfIndex == 0 || l.B.IfIndex == 0 {
			for _, o := range uniq {
				if o.Source == l.Source && o.A.DeviceID == l.A.DeviceID && o.B.DeviceID == l.B.DeviceID &&
					(o.A.IfIndex != 0 && o.B.IfIndex != 0) &&
					(l.A.IfIndex == 0 || l.A.IfIndex == o.A.IfIndex) && (l.B.IfIndex == 0 || l.B.IfIndex == o.B.IfIndex) {
					covered = true
					break
				}
			}
		}
		if !covered {
			out = append(out, l)
		}
	}
	return out
}

// fdbView is what one device learned about the other managed devices.
type fdbView struct {
	// where[y] is the port of the viewing device on which y is learned.
	where map[*node]int
	// on[port] lists the devices learned on a port.
	on   map[int][]*node
	ends map[int]End
}

func (g *graph) view(n *node) *fdbView {
	v := &fdbView{where: map[*node]int{}, on: map[int][]*node{}, ends: map[int]End{}}
	if n.dev.Snap == nil || len(n.dev.Snap.FDB) == 0 {
		return v
	}
	macsOn := map[int]map[string]bool{} // port -> distinct MACs
	devOn := map[*node]map[int]bool{}   // device -> ports it is learned on
	for _, e := range n.dev.Snap.FDB {
		mac := macaddr.NormalizeLoose(e.MAC)
		if mac == "" || e.IfIndex == 0 {
			continue
		}
		end := lagEnd(n, e.IfIndex)
		if macsOn[end.IfIndex] == nil {
			macsOn[end.IfIndex] = map[string]bool{}
		}
		macsOn[end.IfIndex][mac] = true
		v.ends[end.IfIndex] = end
		if y := g.byMAC[mac]; y != nil && y != n {
			if devOn[y] == nil {
				devOn[y] = map[int]bool{}
			}
			devOn[y][end.IfIndex] = true
		}
	}
	for y, ports := range devOn {
		best := 0
		for p := range ports {
			if best == 0 || len(macsOn[p]) < len(macsOn[best]) || (len(macsOn[p]) == len(macsOn[best]) && p < best) {
				best = p
			}
		}
		v.where[y] = best
		v.on[best] = append(v.on[best], y)
	}
	return v
}

func (g *graph) fdbLinks(lldp []Link, paired map[[2]int64]bool) []Link {
	views := map[*node]*fdbView{}
	for _, n := range g.nodes {
		views[n] = g.view(n)
	}
	hasLLDP := func(a, b *node) bool {
		return paired[[2]int64{a.dev.ID, b.dev.ID}] || paired[[2]int64{b.dev.ID, a.dev.ID}]
	}
	// lldpOn[a][port] lists the devices linked to a by LLDP on that port.
	lldpOn := map[*node]map[int][]*node{}
	for _, l := range lldp {
		for _, side := range [][2]End{{l.A, l.B}, {l.B, l.A}} {
			a, b := g.nodeByID(side[0].DeviceID), g.nodeByID(side[1].DeviceID)
			if a == nil || b == nil {
				continue
			}
			if lldpOn[a] == nil {
				lldpOn[a] = map[int][]*node{}
			}
			lldpOn[a][side[0].IfIndex] = append(lldpOn[a][side[0].IfIndex], b)
		}
	}
	var out []Link
	for _, a := range g.nodes {
		va := views[a]
		for _, x := range g.nodes {
			port, ok := va.where[x]
			if !ok || hasLLDP(a, x) {
				continue
			}
			vx := views[x]
			if shadowed(a, x, port, va, vx, lldpOn) {
				continue
			}
			far := End{DeviceID: x.dev.ID}
			if fp, ok := vx.where[a]; ok {
				far = vx.ends[fp]
			}
			out = append(out, orient(va.ends[port], far, SourceFDB))
		}
	}
	return out
}

func (g *graph) nodeByID(id int64) *node {
	for _, n := range g.nodes {
		if n.dev.ID == id {
			return n
		}
	}
	return nil
}

// shadowed reports whether x lies behind another device on a's port.
func shadowed(a, x *node, port int, va, vx *fdbView, lldpOn map[*node]map[int][]*node) bool {
	for _, y := range lldpOn[a][port] {
		if y != x {
			return true
		}
	}
	fp, ok := vx.where[a]
	if !ok {
		return false
	}
	for _, y := range vx.on[fp] {
		if y != a && va.where[y] == port {
			return true
		}
	}
	return false
}
