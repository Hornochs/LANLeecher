package fdb

import (
	"fmt"
	"strings"

	"github.com/gosnmp/gosnmp"

	"github.com/lan-dot-party/lanleecher/internal/snmp"
)

// LLDP-MIB (IEEE 802.1AB) OIDs.
const (
	oidLldpLocPortIDSubtype = "1.0.8802.1.1.2.1.3.7.1.2"
	oidLldpLocPortID        = "1.0.8802.1.1.2.1.3.7.1.3"
	oidLldpLocPortDesc      = "1.0.8802.1.1.2.1.3.7.1.4"
	oidLldpRemSysName       = "1.0.8802.1.1.2.1.4.1.1.9"
)

// IfNames maps ifIndex to interface names (ifName, ifDescr, ifAlias) for
// matching LLDP local port IDs.
type IfNames map[int][]string

// LLDPNeighbors returns the remote system name per ifIndex. A neighbor on a
// port is a strong hint for an uplink (another switch, router or AP), which
// the admin then confirms. Agents without LLDP-MIB return no neighbors.
func LLDPNeighbors(g *gosnmp.GoSNMP, names IfNames) (map[int]string, error) {
	remote, err := snmp.WalkColumn(g, oidLldpRemSysName)
	if err != nil {
		return nil, fmt.Errorf("walk lldpRemSysName: %w", err)
	}
	if len(remote) == 0 {
		return nil, nil
	}
	localToIf, err := lldpLocalPorts(g, names)
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	for _, r := range remote {
		// Index: lldpRemTimeMark.lldpRemLocalPortNum.lldpRemIndex
		if len(r.Index) != 3 {
			continue
		}
		ifIndex, ok := localToIf[r.Index[1]]
		if !ok {
			continue
		}
		name := strings.TrimSpace(snmp.String(r.PDU))
		if name == "" {
			name = "?"
		}
		if prev := out[ifIndex]; prev != "" && !strings.Contains(prev, name) {
			name = prev + ", " + name
		}
		out[ifIndex] = name
	}
	return out, nil
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
