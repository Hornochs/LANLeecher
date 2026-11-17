// Package collect gathers absolute per-MAC byte counters on a Linux host or a
// Proxmox VE node. The numbers are reported in the MAC's point of view as the
// LAN switch sees it: Down is traffic sent TO the MAC, Up is traffic sent BY it.
package collect

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"sort"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

// Interface is one MAC address with its counters.
type Interface struct {
	MAC    macaddr.MAC
	IfName string // kernel interface name, e.g. "eno1", "vmbr0", "tap101i0"
	Kind   string // "host", "vm" or "ct"
	Name   string // VM/CT name from the Proxmox config (guests only)
	VMID   int
	VLAN   int // tag= from the Proxmox net line, 0 if none
	Down   uint64
	Up     uint64
	IPs    []netip.Addr
}

// Options controls a collection run.
type Options struct {
	Root    string   // filesystem root for /sys and /etc/pve; "" means "/"
	Include []string // filepath.Match globs on IfName; empty = all
	Exclude []string // globs; applied after Include
	// Addrs returns the IP addresses of a host interface; nil uses
	// net.InterfaceByName(...).Addrs().
	Addrs func(ifname string) []netip.Addr
}

const maxIPs = 8

func (o Options) root() string {
	if o.Root == "" {
		return "/"
	}
	return o.Root
}

func (o Options) path(parts ...string) string {
	return filepath.Join(append([]string{o.root()}, parts...)...)
}

// allowed applies Include and Exclude to an interface name.
func (o Options) allowed(ifname string) bool {
	if len(o.Include) > 0 && !matchAny(o.Include, ifname) {
		return false
	}
	return !matchAny(o.Exclude, ifname)
}

func matchAny(globs []string, name string) bool {
	for _, g := range globs {
		if ok, err := filepath.Match(g, name); err == nil && ok {
			return true
		}
	}
	return false
}

func (o Options) addrs(ifname string) []netip.Addr {
	var raw []netip.Addr
	if o.Addrs != nil {
		raw = o.Addrs(ifname)
	} else if ifc, err := net.InterfaceByName(ifname); err == nil {
		as, _ := ifc.Addrs()
		for _, a := range as {
			var ip netip.Addr
			var ok bool
			switch v := a.(type) {
			case *net.IPNet:
				ip, ok = netip.AddrFromSlice(v.IP)
			case *net.IPAddr:
				ip, ok = netip.AddrFromSlice(v.IP)
			}
			if ok {
				raw = append(raw, ip)
			}
		}
	}
	return filterIPs(raw)
}

// filterIPs unmaps IPv4-mapped addresses and drops link-local, loopback,
// multicast and unspecified ones, duplicates, and everything past maxIPs.
func filterIPs(in []netip.Addr) []netip.Addr {
	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, ip := range in {
		ip = ip.Unmap().WithZone("")
		if !ip.IsValid() || ip.IsLinkLocalUnicast() || ip.IsLoopback() ||
			ip.IsMulticast() || ip.IsUnspecified() || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, ip)
		if len(out) == maxIPs {
			break
		}
	}
	return out
}

func sortInterfaces(l []Interface) {
	sort.Slice(l, func(i, j int) bool { return l[i].IfName < l[j].IfName })
}

// Detect returns "proxmox" if <root>/etc/pve/local is a directory, else "linux".
func Detect(o Options) string {
	if isDir(o.path("etc/pve/local")) {
		return "proxmox"
	}
	return "linux"
}

// Collect runs the collector for mode "auto", "linux" or "proxmox".
func Collect(mode string, o Options) ([]Interface, error) {
	switch mode {
	case "", "auto":
		return Collect(Detect(o), o)
	case "linux":
		return Linux(o)
	case "proxmox":
		return Proxmox(o)
	}
	return nil, fmt.Errorf("unknown collect mode %q", mode)
}
