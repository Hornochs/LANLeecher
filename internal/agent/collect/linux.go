package collect

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

const arphrdEther = 1

// guestDevRe matches Proxmox guest devices (tap<vmid>i<n>, veth<vmid>i<n>).
var guestDevRe = regexp.MustCompile(`^(tap|veth)(\d+)i(\d+)$`)

var skipPrefixes = []string{"veth", "docker", "virbr", "fwbr", "fwpr", "fwln"}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// exists reports whether p exists without following a final symlink, so
// dangling links (as in test fixtures) still count.
func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func readTrim(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readCounter(p string) (uint64, error) {
	return strconv.ParseUint(readTrim(p), 10, 64)
}

// hostDirection maps the kernel counters of a host interface to the MAC's
// point of view. The host's own interface IS the MAC, so received bytes were
// sent TO it (Down) and transmitted bytes were sent BY it (Up).
func hostDirection(rx, tx uint64) (down, up uint64) {
	return rx, tx
}

// kind ranks for MAC deduplication: bridge > bond > physical.
const (
	rankPhysical = iota + 1
	rankBond
	rankBridge
)

type hostIf struct {
	Interface
	rank int
}

// Linux returns the host's own MACs from <root>/sys/class/net.
func Linux(o Options) ([]Interface, error) {
	base := o.path("sys/class/net")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", base, err)
	}
	best := map[macaddr.MAC]hostIf{}
	for _, e := range entries {
		name := e.Name()
		if !o.allowed(name) {
			continue
		}
		h, ok := hostInterface(o, name)
		if !ok {
			continue
		}
		cur, dup := best[h.MAC]
		if !dup || h.rank > cur.rank || (h.rank == cur.rank && h.IfName < cur.IfName) {
			best[h.MAC] = h
		}
	}
	out := make([]Interface, 0, len(best))
	for _, h := range best {
		h.IPs = o.addrs(h.IfName)
		out = append(out, h.Interface)
	}
	sortInterfaces(out)
	return out, nil
}

func hostInterface(o Options, name string) (hostIf, bool) {
	var zero hostIf
	for _, p := range skipPrefixes {
		if strings.HasPrefix(name, p) {
			return zero, false
		}
	}
	if m := guestDevRe.FindStringSubmatch(name); m != nil {
		return zero, false
	}
	dir := o.path("sys/class/net", name)
	if readTrim(dir+"/type") != strconv.Itoa(arphrdEther) {
		return zero, false
	}
	if readTrim(dir+"/operstate") == "down" {
		return zero, false
	}
	if exists(dir + "/master") { // slave: accounted on its master
		return zero, false
	}
	if ueventValue(dir+"/uevent", "DEVTYPE") == "vlan" {
		return zero, false
	}
	mac, err := macaddr.Parse(readTrim(dir + "/address"))
	if err != nil || mac.IsZero() || mac.IsMulticast() {
		return zero, false
	}
	rank := rankPhysical
	switch {
	case isDir(dir + "/bridge"):
		if !bridgeHasUplink(o, dir+"/brif") {
			return zero, false
		}
		rank = rankBridge
	case isDir(dir + "/bonding"):
		rank = rankBond
	}
	rx, err1 := readCounter(dir + "/statistics/rx_bytes")
	tx, err2 := readCounter(dir + "/statistics/tx_bytes")
	if err1 != nil || err2 != nil {
		return zero, false
	}
	down, up := hostDirection(rx, tx)
	return hostIf{Interface{MAC: mac, IfName: name, Kind: "host", Down: down, Up: up}, rank}, true
}

// bridgeHasUplink reports whether a bridge has a physical or bond port.
func bridgeHasUplink(o Options, brif string) bool {
	ports, err := os.ReadDir(brif)
	if err != nil {
		return false
	}
	for _, p := range ports {
		pd := o.path("sys/class/net", p.Name())
		if exists(pd+"/device") || isDir(pd+"/bonding") {
			return true
		}
	}
	return false
}

func ueventValue(path, key string) string {
	for _, l := range strings.Split(readTrim(path), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && k == key {
			return v
		}
	}
	return ""
}
