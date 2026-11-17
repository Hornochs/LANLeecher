package collect

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

const maxConfSize = 1 << 20

var (
	netLineRe = regexp.MustCompile(`^net(\d+):\s*(.*)$`)
	qemuModel = map[string]bool{
		"virtio": true, "e1000": true, "e1000e": true, "vmxnet3": true, "rtl8139": true,
		"ne2k_pci": true, "pcnet": true, "i82551": true, "i82557b": true, "i82559er": true,
	}
)

// guestDirection maps tap/veth counters to the guest's point of view. The
// device is the HOST end of the guest NIC: what the host transmits on it is
// received by the guest (Down), what the host receives is sent by the guest
// (Up). This is the same swap Proxmox applies for netin/netout.
func guestDirection(rx, tx uint64) (down, up uint64) {
	return tx, rx
}

// Proxmox returns the host's own MACs plus all running guests.
func Proxmox(o Options) ([]Interface, error) {
	out, err := Linux(o)
	if err != nil {
		return nil, err
	}
	out = append(out, guests(o, "qemu-server", "vm", "tap", "name")...)
	out = append(out, guests(o, "lxc", "ct", "veth", "hostname")...)
	sortInterfaces(out)
	return out, nil
}

func guests(o Options, dir, kind, devPrefix, nameKey string) []Interface {
	files, _ := filepath.Glob(o.path("etc/pve/local", dir, "*.conf"))
	var out []Interface
	for _, f := range files {
		vmid, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(f), ".conf"))
		if err != nil {
			continue
		}
		st, err := os.Stat(f)
		if err != nil || st.Size() > maxConfSize {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		name, nics := parseGuestConf(string(b), nameKey, kind == "vm")
		for _, n := range nics {
			dev := fmt.Sprintf("%s%di%d", devPrefix, vmid, n.index)
			if !o.allowed(dev) {
				continue
			}
			sd := o.path("sys/class/net", dev, "statistics")
			if !isDir(sd) {
				continue // not running
			}
			rx, err1 := readCounter(sd + "/rx_bytes")
			tx, err2 := readCounter(sd + "/tx_bytes")
			if err1 != nil || err2 != nil {
				continue
			}
			down, up := guestDirection(rx, tx)
			out = append(out, Interface{
				MAC: n.mac, IfName: dev, Kind: kind, Name: name, VMID: vmid,
				VLAN: n.vlan, Down: down, Up: up, IPs: filterIPs(n.ips),
			})
		}
	}
	return out
}

type guestNIC struct {
	index int
	mac   macaddr.MAC
	vlan  int
	ips   []netip.Addr
}

// parseGuestConf parses the main section (before the first "[" line) of a
// Proxmox guest config. Malformed lines are skipped.
func parseGuestConf(conf, nameKey string, qemu bool) (string, []guestNIC) {
	var name string
	var nics []guestNIC
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		if v, ok := strings.CutPrefix(line, nameKey+":"); ok {
			name = strings.TrimSpace(v)
			continue
		}
		m := netLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if nic, ok := parseNIC(idx, m[2], qemu); ok {
			nics = append(nics, nic)
		}
	}
	return name, nics
}

func parseNIC(idx int, val string, qemu bool) (guestNIC, bool) {
	n := guestNIC{index: idx}
	var haveMAC bool
	for _, part := range strings.Split(val, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch {
		case k == "macaddr" || (qemu && qemuModel[k]) || (!qemu && k == "hwaddr"):
			if mac, err := macaddr.Parse(v); err == nil && !mac.IsZero() && !mac.IsMulticast() {
				n.mac, haveMAC = mac, true
			}
		case k == "tag":
			if t, err := strconv.Atoi(v); err == nil && t > 0 && t < 4095 {
				n.vlan = t
			}
		case !qemu && (k == "ip" || k == "ip6"):
			if p, err := netip.ParsePrefix(v); err == nil {
				n.ips = append(n.ips, p.Addr())
			}
		}
	}
	return n, haveMAC
}
