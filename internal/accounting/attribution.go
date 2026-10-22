package accounting

import (
	"sort"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

// Role describes what is connected to a switch port.
type Role string

const (
	// RoleClient is a normal guest port (default).
	RoleClient Role = "client"
	// RoleMultiClient is a port with a small switch and several guests.
	RoleMultiClient Role = "multi-client"
	// RoleServer is a server port; attributed like a client, hidden in rankings by default.
	RoleServer Role = "server"
	// RoleUplink is a trunk to another switch or router; never attributed to MACs.
	RoleUplink Role = "uplink"
)

// Roles lists all valid roles in UI order.
var Roles = []Role{RoleClient, RoleMultiClient, RoleServer, RoleUplink}

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	for _, v := range Roles {
		if r == v {
			return true
		}
	}
	return false
}

// HiddenByDefault reports whether ports with this role start hidden in rankings.
func (r Role) HiddenByDefault() bool {
	return r == RoleServer || r == RoleUplink
}

// TargetKind says where a port delta is booked.
type TargetKind string

const (
	// TargetMAC credits a single MAC (and through it, its device).
	TargetMAC TargetKind = "mac"
	// TargetPortGroup credits the shared entry of a multi-client port.
	TargetPortGroup TargetKind = "port_group"
	// TargetUnassigned books traffic on the port when no MAC is known.
	TargetUnassigned TargetKind = "unassigned"
	// TargetNone books nothing beyond the port total (uplinks).
	TargetNone TargetKind = "none"
)

// Target is the outcome of Attribute.
type Target struct {
	Kind TargetKind
	// MAC is set for TargetMAC.
	MAC macaddr.MAC
	// Members lists the MACs of a port group (sorted), for TargetPortGroup.
	Members []macaddr.MAC
}

// Attribute decides who is credited for traffic on a port.
//
//   - uplink: nobody (port total only), or traffic behind it is counted twice.
//   - client/server with exactly one MAC: that MAC.
//   - client/server with several MACs, or multi-client: the port group.
//   - no MAC learned: unassigned on the port. It is never credited to a MAC
//     that shows up later.
//
// Every delta is additionally booked on the port itself, whatever the target.
func Attribute(role Role, macs []macaddr.MAC) Target {
	if role == RoleUplink {
		return Target{Kind: TargetNone}
	}
	uniq := dedupe(macs)
	switch {
	case role == RoleMultiClient && len(uniq) > 0:
		return Target{Kind: TargetPortGroup, Members: uniq}
	case len(uniq) == 0:
		return Target{Kind: TargetUnassigned}
	case len(uniq) == 1:
		return Target{Kind: TargetMAC, MAC: uniq[0]}
	default:
		return Target{Kind: TargetPortGroup, Members: uniq}
	}
}

func dedupe(macs []macaddr.MAC) []macaddr.MAC {
	seen := make(map[macaddr.MAC]bool, len(macs))
	out := make([]macaddr.MAC, 0, len(macs))
	for _, m := range macs {
		if m.IsZero() || m.IsMulticast() || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
