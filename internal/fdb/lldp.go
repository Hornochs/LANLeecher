package fdb

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/snmp"
)

// LLDP-MIB (IEEE 802.1AB) OIDs.
const (
	oidLldpLocPortIDSubtype = "1.0.8802.1.1.2.1.3.7.1.2"
	oidLldpLocPortID        = "1.0.8802.1.1.2.1.3.7.1.3"
	oidLldpLocPortDesc      = "1.0.8802.1.1.2.1.3.7.1.4"
	oidLldpRemSysName       = "1.0.8802.1.1.2.1.4.1.1.9"
	oidLldpRemChassisSub    = "1.0.8802.1.1.2.1.4.1.1.4"
	oidLldpRemChassisID     = "1.0.8802.1.1.2.1.4.1.1.5"
	oidLldpRemPortID        = "1.0.8802.1.1.2.1.4.1.1.7"
	oidLldpRemPortDesc      = "1.0.8802.1.1.2.1.4.1.1.8"
	oidLldpRemSysCap        = "1.0.8802.1.1.2.1.4.1.1.12"
	oidLldpLocChassisSub    = "1.0.8802.1.1.2.1.3.1.0"
	oidLldpLocChassisID     = "1.0.8802.1.1.2.1.3.2.0"
	oidBaseBridgeAddress    = "1.3.6.1.2.1.17.1.1.0"

	// IEEE8023-LAG-MIB dot3adAggPortAttachedAggID: the aggregator (ifIndex)
	// a port is attached to.
	oidAggPortAttachedAggID = "1.2.840.10006.300.43.1.2.1.1.13"
	// TPLINK-DOT1Q-VLAN-MIB tpVlanPortLag, indexed by port: "LAG1" or "-".
	oidTPVlanPortLag = "1.3.6.1.4.1.11863.6.14.1.1.1.1.5"
)

// IfNames maps ifIndex to interface names (ifName, ifDescr, ifAlias) for
// matching LLDP local port IDs.
type IfNames map[int][]string

// Neighbor is a system seen by LLDP on a port.
type Neighbor struct {
	SysName   string
	PortID    string
	PortDesc  string
	ChassisID string // a MAC address in the form aa:bb:cc:dd:ee:ff when the ID is one
	// Bridge is set when the neighbor announces bridge capability.
	Bridge bool
}

// LLDPAll returns all LLDP neighbors per ifIndex, ordered as walked (by
// remote index). Agents without LLDP-MIB return an empty map. Only the walk
// of lldpRemSysName and of the local port table can fail; the other columns
// are optional.
func LLDPAll(g *gosnmp.GoSNMP, names IfNames) (map[int][]Neighbor, error) {
	sysNames, err := snmp.WalkColumn(g, oidLldpRemSysName)
	if err != nil {
		return nil, fmt.Errorf("walk lldpRemSysName: %w", err)
	}
	// Every other column is best effort.
	column := func(oid string) map[[3]int]gosnmp.SnmpPDU {
		out := map[[3]int]gosnmp.SnmpPDU{}
		rows, err := snmp.WalkColumn(g, oid)
		if err != nil {
			return out
		}
		for _, r := range rows {
			if len(r.Index) == 3 {
				out[[3]int{r.Index[0], r.Index[1], r.Index[2]}] = r.PDU
			}
		}
		return out
	}
	names3 := map[[3]int]gosnmp.SnmpPDU{}
	for _, r := range sysNames {
		// Index: lldpRemTimeMark.lldpRemLocalPortNum.lldpRemIndex
		if len(r.Index) == 3 {
			names3[[3]int{r.Index[0], r.Index[1], r.Index[2]}] = r.PDU
		}
	}
	if len(names3) == 0 {
		return map[int][]Neighbor{}, nil
	}
	chassisSub, chassisID := column(oidLldpRemChassisSub), column(oidLldpRemChassisID)
	portIDs, portDescs, caps := column(oidLldpRemPortID), column(oidLldpRemPortDesc), column(oidLldpRemSysCap)
	localToIf, err := lldpLocalPorts(g, names)
	if err != nil {
		return nil, err
	}
	keys := make([][3]int, 0, len(names3))
	for k := range names3 {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		for n := range keys[i] {
			if keys[i][n] != keys[j][n] {
				return keys[i][n] < keys[j][n]
			}
		}
		return false
	})
	out := map[int][]Neighbor{}
	for _, k := range keys {
		ifIndex, ok := localToIf[k[1]]
		if !ok {
			continue
		}
		n := Neighbor{SysName: strings.TrimSpace(snmp.String(names3[k]))}
		if p, ok := portIDs[k]; ok {
			n.PortID = printableID(p)
		}
		if p, ok := portDescs[k]; ok {
			n.PortDesc = strings.TrimSpace(snmp.String(p))
		}
		if p, ok := chassisID[k]; ok {
			sub, _ := snmp.Int(chassisSub[k])
			n.ChassisID = chassisString(sub, p)
		}
		if b := snmp.Bytes(caps[k]); len(b) > 0 && b[0]&0x20 != 0 { // bit 2 = bridge
			n.Bridge = true
		}
		out[ifIndex] = append(out[ifIndex], n)
	}
	return out, nil
}

// Names joins the system names of the neighbors on one port for display. A
// neighbor without a name shows as "?".
func Names(ns []Neighbor) string {
	name := ""
	for _, n := range ns {
		s := n.SysName
		if s == "" {
			s = "?"
		}
		if name != "" && strings.Contains(name, s) {
			continue
		}
		if name != "" {
			s = name + ", " + s
		}
		name = s
	}
	return name
}

// Preferred picks the neighbor to draw on a port: the first one that is a
// bridge, else the first one.
func Preferred(ns []Neighbor) (Neighbor, bool) {
	for _, n := range ns {
		if n.Bridge {
			return n, true
		}
	}
	if len(ns) > 0 {
		return ns[0], true
	}
	return Neighbor{}, false
}

// LLDPNeighbors returns the remote system name per ifIndex. A neighbor on a
// port is a strong hint for an uplink (another switch, router or AP), which
// the admin then confirms. Agents without LLDP-MIB return no neighbors.
func LLDPNeighbors(g *gosnmp.GoSNMP, names IfNames) (map[int]string, error) {
	all, err := LLDPAll(g, names)
	if err != nil {
		return nil, err
	}
	return NeighborNames(all), nil
}

// NeighborNames returns the display names of LLDPAll's result.
func NeighborNames(all map[int][]Neighbor) map[int]string {
	out := map[int]string{}
	for ifIndex, ns := range all {
		out[ifIndex] = Names(ns)
	}
	return out
}

// LLDPDetails returns the neighbor per ifIndex (see Preferred).
func LLDPDetails(g *gosnmp.GoSNMP, names IfNames) (map[int]Neighbor, error) {
	all, err := LLDPAll(g, names)
	if err != nil {
		return nil, err
	}
	return PreferredNeighbors(all), nil
}

// PreferredNeighbors reduces LLDPAll's result to one neighbor per port.
func PreferredNeighbors(all map[int][]Neighbor) map[int]Neighbor {
	out := map[int]Neighbor{}
	for ifIndex, ns := range all {
		if n, ok := Preferred(ns); ok {
			out[ifIndex] = n
		}
	}
	return out
}

// LocalChassis reads the switch's own LLDP chassis ID and its bridge base
// MAC (dot1dBaseBridgeAddress). Either is "" when the agent has none.
func LocalChassis(g *gosnmp.GoSNMP) (chassisID, baseMAC string) {
	get := func(oid string) (gosnmp.SnmpPDU, bool) {
		res, err := g.Get([]string{oid})
		if err != nil || len(res.Variables) != 1 {
			return gosnmp.SnmpPDU{}, false
		}
		switch res.Variables[0].Type {
		case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.Null:
			return gosnmp.SnmpPDU{}, false
		}
		return res.Variables[0], true
	}
	if id, ok := get(oidLldpLocChassisID); ok {
		sub := 0
		if p, ok := get(oidLldpLocChassisSub); ok {
			sub, _ = snmp.Int(p)
		}
		chassisID = chassisString(sub, id)
	}
	if p, ok := get(oidBaseBridgeAddress); ok {
		if m, ok := macaddr.FromBytes(snmp.Bytes(p)); ok && !m.IsZero() {
			baseMAC = m.String()
		}
	}
	return chassisID, baseMAC
}

// lagNumber matches the number at the end of a LAG interface name.
var lagNumber = regexp.MustCompile(`(\d+)\s*$`)

// LAGMembers returns the link aggregation each member port belongs to
// (member ifIndex to LAG ifIndex). It reads dot3adAggPortAttachedAggID; if
// that is empty it tries the TP-Link table tpVlanPortLag, whose values name
// the LAG ("LAG1"). Such a name is matched to the LAG interface (one of
// lagIfaces, ifIndex to names) whose name ends in the same number. The map is
// empty (not nil) when the switch reports no memberships.
func LAGMembers(g *gosnmp.GoSNMP, lagIfaces IfNames) (map[int]int, error) {
	out := map[int]int{}
	rows, err := snmp.WalkColumn(g, oidAggPortAttachedAggID)
	if err != nil {
		return nil, fmt.Errorf("walk dot3adAggPortAttachedAggID: %w", err)
	}
	for _, r := range rows {
		agg, ok := snmp.Int(r.PDU)
		if len(r.Index) != 1 || !ok || agg == 0 || agg == r.Index[0] {
			continue
		}
		out[r.Index[0]] = agg
	}
	if len(out) > 0 {
		return out, nil
	}
	rows, err = snmp.WalkColumn(g, oidTPVlanPortLag)
	if err != nil {
		return nil, fmt.Errorf("walk tpVlanPortLag: %w", err)
	}
	byNumber := map[int]int{}
	for idx, names := range lagIfaces {
		for _, n := range names {
			if m := lagNumber.FindStringSubmatch(n); m != nil {
				num, _ := strconv.Atoi(m[1])
				if cur, dup := byNumber[num]; !dup || idx < cur {
					byNumber[num] = idx
				}
				break
			}
		}
	}
	for _, r := range rows {
		v := strings.ToUpper(strings.TrimSpace(snmp.String(r.PDU)))
		m := lagNumber.FindStringSubmatch(v)
		if len(r.Index) != 1 || !strings.HasPrefix(v, "LAG") || m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		if agg, ok := byNumber[num]; ok && agg != r.Index[0] {
			out[r.Index[0]] = agg
		}
	}
	return out, nil
}

// chassisString formats an LLDP chassis ID. Subtype 4 (macAddress) and six
// raw bytes give the MAC format; other IDs are shown as trimmed text, or as
// hex bytes when they are not printable.
func chassisString(subtype int, p gosnmp.SnmpPDU) string {
	b, ok := p.Value.([]byte)
	if !ok {
		return strings.TrimSpace(snmp.String(p))
	}
	if len(b) == 6 && (subtype == 4 || !printableText(b)) {
		m, _ := macaddr.FromBytes(b)
		return m.String()
	}
	b = []byte(strings.TrimRight(string(b), "\x00"))
	if !printableText(b) {
		return printableID(p)
	}
	s := strings.TrimSpace(string(b))
	if subtype == 4 {
		if m := macaddr.NormalizeLoose(s); m != "" {
			return m
		}
	}
	return s
}

func printableText(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// printableID shows binary IDs as hex bytes and text as text.
func printableID(p gosnmp.SnmpPDU) string {
	b, ok := p.Value.([]byte)
	if !ok {
		return strings.TrimSpace(snmp.String(p))
	}
	if !printableText(b) {
		parts := make([]string, len(b))
		for i, x := range b {
			parts[i] = fmt.Sprintf("%02x", x)
		}
		return strings.Join(parts, ":")
	}
	return string(b)
}

// lldpLocalPorts maps lldpLocPortNum to ifIndex. The port ID (subtype
// interfaceName or local) or description is matched against interface names;
// as a last resort the port number is taken as ifIndex.
func lldpLocalPorts(g *gosnmp.GoSNMP, names IfNames) (map[int]int, error) {
	byName := map[string]int{}
	for idx, ns := range names {
		for _, n := range ns {
			if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
				if _, dup := byName[n]; !dup {
					byName[n] = idx
				}
			}
		}
	}
	out := map[int]int{}
	for _, col := range []string{oidLldpLocPortID, oidLldpLocPortDesc} {
		rows, err := snmp.WalkColumn(g, col)
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", col, err)
		}
		for _, r := range rows {
			if len(r.Index) != 1 {
				continue
			}
			if _, done := out[r.Index[0]]; done {
				continue
			}
			if idx, ok := byName[strings.ToLower(strings.TrimSpace(snmp.String(r.PDU)))]; ok {
				out[r.Index[0]] = idx
			}
		}
	}
	subtypes, err := snmp.WalkColumn(g, oidLldpLocPortIDSubtype)
	if err != nil {
		return nil, err
	}
	for _, r := range subtypes {
		if len(r.Index) != 1 {
			continue
		}
		if _, done := out[r.Index[0]]; !done {
			if _, exists := names[r.Index[0]]; exists {
				out[r.Index[0]] = r.Index[0]
			}
		}
	}
	return out, nil
}
