// Package collector reads interface counters and port names from switches.
package collector

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/lan-dot-party/lanleecher/internal/snmp"
)

// Interface is one row of the IF-MIB as seen in a poll.
type Interface struct {
	IfIndex int
	Name    string
	Alias   string
	Descr   string
	Type    int
	OperUp  bool
	// SpeedMbps is ifHighSpeed.
	SpeedMbps uint64
	// HasHC is false when the port has no 64-bit counters; it is then not counted.
	HasHC bool
	// HCIn is ifHCInOctets (client upload), HCOut is ifHCOutOctets (client download).
	HCIn, HCOut uint64
	// Discontinuity is ifCounterDiscontinuityTime in TimeTicks.
	Discontinuity uint32
}

// Physical reports whether the interface is a real port or a LAG, as opposed
// to a VLAN interface, loopback or tunnel. Some vendors (e.g. TP-Link) report
// ifType ethernetCsmacd for everything, so names are checked as well.
func (i *Interface) Physical() bool {
	return IsPhysical(i.Type) && !virtualName.MatchString(i.Name) && !virtualName.MatchString(i.Descr)
}

// IsLAG reports a link aggregation (port-channel).
func (i *Interface) IsLAG() bool {
	return i.Type == ifTypeLAG || lagName.MatchString(i.Name) || lagName.MatchString(i.Descr)
}

var (
	virtualName = regexp.MustCompile(`(?i)^(vlan|vl\d|svi|loopback|lo\d|null|tunnel|tun\d|mgmt|management|cpu|stack|vif|br\d|bridge\b|irb|bvi)`)
	lagName     = regexp.MustCompile(`(?i)^(port-?channel|po\d|ae\d|bond|lag|trunk|bridge-aggregation|link aggregation|aggregat)`)
)

// Snapshot is the result of one counter poll.
type Snapshot struct {
	// SysUptime in TimeTicks, read right before the counters.
	SysUptime  uint32
	At         time.Time
	Interfaces map[int]*Interface
}

// System is basic agent information for the connection test.
type System struct {
	Descr  string
	Name   string
	Uptime time.Duration
}

// Collector reads counters from one device. SNMP is the only implementation.
type Collector interface {
	// System returns sysDescr, sysName and sysUpTime.
	System(ctx context.Context) (System, error)
	// Poll reads all interface counters.
	Poll(ctx context.Context) (*Snapshot, error)
}

// IF-MIB / RFC 2863 OIDs.
const (
	oidSysDescr  = "1.3.6.1.2.1.1.1.0"
	oidSysUpTime = "1.3.6.1.2.1.1.3.0"
	oidSysName   = "1.3.6.1.2.1.1.5.0"

	oidIfDescr      = "1.3.6.1.2.1.2.2.1.2"
	oidIfType       = "1.3.6.1.2.1.2.2.1.3"
	oidIfOperStatus = "1.3.6.1.2.1.2.2.1.8"
	oidIfName       = "1.3.6.1.2.1.31.1.1.1.1"
	oidIfHCIn       = "1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOut      = "1.3.6.1.2.1.31.1.1.1.10"
	oidIfHighSpeed  = "1.3.6.1.2.1.31.1.1.1.15"
	oidIfAlias      = "1.3.6.1.2.1.31.1.1.1.18"
	oidIfDiscont    = "1.3.6.1.2.1.31.1.1.1.19"
)

// ifType values (IANAifType-MIB).
const (
	ifTypeEthernet = 6
	ifTypeFastEth  = 62
	ifTypeGigEth   = 117
	ifTypeLAG      = 161
)

// IsPhysical reports physical ports and LAGs. Unknown type 0 counts as physical.
func IsPhysical(ifType int) bool {
	switch ifType {
	case 0, ifTypeEthernet, ifTypeFastEth, ifTypeGigEth, ifTypeLAG:
		return true
	}
	return false
}

// IsLAGType reports a link aggregation interface type.
func IsLAGType(ifType int) bool { return ifType == ifTypeLAG }

// SNMP is the SNMP collector. It opens a new connection per call, so a stuck
// or rebooting switch never leaves a broken session behind.
type SNMP struct {
	Target snmp.Target
}

// System implements Collector.
func (c *SNMP) System(ctx context.Context) (System, error) {
	g, err := snmp.Dial(ctx, c.Target, 0)
	if err != nil {
		return System{}, err
	}
	defer snmp.Close(g)
	res, err := g.Get([]string{oidSysDescr, oidSysName, oidSysUpTime})
	if err != nil {
		return System{}, fmt.Errorf("get system: %w", err)
	}
	var s System
	for _, v := range res.Variables {
		switch v.Name {
		case "." + oidSysDescr:
			s.Descr = snmp.String(v)
		case "." + oidSysName:
			s.Name = snmp.String(v)
		case "." + oidSysUpTime:
			t, _ := snmp.Uint64(v)
			s.Uptime = time.Duration(t) * 10 * time.Millisecond
		}
	}
	return s, nil
}

// Poll implements Collector.
func (c *SNMP) Poll(ctx context.Context) (*Snapshot, error) {
	g, err := snmp.Dial(ctx, c.Target, 0)
	if err != nil {
		return nil, err
	}
	defer snmp.Close(g)
	return PollWith(g)
}

// PollWith polls using an existing connection.
func PollWith(g *gosnmp.GoSNMP) (*Snapshot, error) {
	res, err := g.Get([]string{oidSysUpTime})
	if err != nil {
		return nil, fmt.Errorf("get sysUpTime: %w", err)
	}
	if len(res.Variables) != 1 {
		return nil, fmt.Errorf("get sysUpTime: no value")
	}
	up, ok := snmp.Uint64(res.Variables[0])
	if !ok {
		return nil, fmt.Errorf("get sysUpTime: unexpected type %v", res.Variables[0].Type)
	}
	snap := &Snapshot{SysUptime: uint32(up), At: time.Now().UTC(), Interfaces: map[int]*Interface{}}
	get := func(idx int) *Interface {
		i, ok := snap.Interfaces[idx]
		if !ok {
			i = &Interface{IfIndex: idx}
			snap.Interfaces[idx] = i
		}
		return i
	}

	columns := []struct {
		oid string
		set func(*Interface, gosnmp.SnmpPDU)
	}{
		{oidIfDescr, func(i *Interface, p gosnmp.SnmpPDU) { i.Descr = snmp.String(p) }},
		{oidIfType, func(i *Interface, p gosnmp.SnmpPDU) { i.Type, _ = snmp.Int(p) }},
		{oidIfOperStatus, func(i *Interface, p gosnmp.SnmpPDU) { v, _ := snmp.Int(p); i.OperUp = v == 1 }},
		{oidIfName, func(i *Interface, p gosnmp.SnmpPDU) { i.Name = snmp.String(p) }},
		{oidIfAlias, func(i *Interface, p gosnmp.SnmpPDU) { i.Alias = snmp.String(p) }},
		{oidIfHighSpeed, func(i *Interface, p gosnmp.SnmpPDU) { i.SpeedMbps, _ = snmp.Uint64(p) }},
		{oidIfHCIn, func(i *Interface, p gosnmp.SnmpPDU) {
			if p.Type == gosnmp.Counter64 {
				i.HCIn, _ = snmp.Uint64(p)
				i.HasHC = true
			}
		}},
		{oidIfHCOut, func(i *Interface, p gosnmp.SnmpPDU) {
			if p.Type == gosnmp.Counter64 {
				i.HCOut, _ = snmp.Uint64(p)
			}
		}},
		{oidIfDiscont, func(i *Interface, p gosnmp.SnmpPDU) { v, _ := snmp.Uint64(p); i.Discontinuity = uint32(v) }},
	}
	for _, col := range columns {
		rows, err := snmp.WalkColumn(g, col.oid)
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", col.oid, err)
		}
		for _, r := range rows {
			if len(r.Index) != 1 {
				continue
			}
			col.set(get(r.Index[0]), r.PDU)
		}
	}
	return snap, nil
}
