package resolver

import (
	"bufio"
	"net/netip"
	"os"
	"strings"

	"github.com/gosnmp/gosnmp"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/snmp"
)

const (
	// ipNetToPhysicalPhysAddress (IP-MIB, IPv4 and IPv6).
	oidIPNetToPhysical = "1.3.6.1.2.1.4.35.1.4"
	// ipNetToMediaPhysAddress (RFC 1213, IPv4 only), fallback.
	oidIPNetToMedia = "1.3.6.1.2.1.4.22.1.2"
)

// ReadSNMPARP reads the ARP/neighbor table of a router or L3 switch.
func ReadSNMPARP(g *gosnmp.GoSNMP) ([]Pair, error) {
	rows, err := snmp.WalkColumn(g, oidIPNetToPhysical)
	if err != nil {
		return nil, err
	}
	var out []Pair
	for _, r := range rows {
		// Index: ifIndex.addrType.length.addr...
		if len(r.Index) < 3 {
			continue
		}
		n := r.Index[2]
		if len(r.Index) != 3+n {
			continue
		}
		raw := make([]byte, n)
		for i := 0; i < n; i++ {
			raw[i] = byte(r.Index[3+i])
		}
		ip, ok := netip.AddrFromSlice(raw)
		mac, okMAC := macaddr.FromBytes(snmp.Bytes(r.PDU))
		if ok && okMAC {
			out = append(out, Pair{MAC: mac, IP: ip.Unmap()})
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	rows, err = snmp.WalkColumn(g, oidIPNetToMedia)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		// Index: ifIndex.a.b.c.d
		if len(r.Index) != 5 {
			continue
		}
		ip := netip.AddrFrom4([4]byte{byte(r.Index[1]), byte(r.Index[2]), byte(r.Index[3]), byte(r.Index[4])})
		if mac, ok := macaddr.FromBytes(snmp.Bytes(r.PDU)); ok {
			out = append(out, Pair{MAC: mac, IP: ip})
		}
	}
	return out, nil
}

// ReadLocalNeigh reads the IPv4 neighbor table of the LANLeecher host. It
// only sees clients in the host's own segment.
func ReadLocalNeigh() ([]Pair, error) {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Pair
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		// IP address  HW type  Flags  HW address  Mask  Device
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[2] == "0x0" {
			continue
		}
		ip, err := netip.ParseAddr(fields[0])
		if err != nil {
			continue
		}
		mac, err := macaddr.Parse(fields[3])
		if err != nil || mac.IsZero() {
			continue
		}
		out = append(out, Pair{MAC: mac, IP: ip})
	}
	return out, sc.Err()
}
