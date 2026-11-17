package fdb

import (
	"testing"

	"github.com/gosnmp/gosnmp"
)

func TestChassisString(t *testing.T) {
	pdu := func(v any) gosnmp.SnmpPDU { return gosnmp.SnmpPDU{Type: gosnmp.OctetString, Value: v} }
	for _, c := range []struct {
		sub  int
		in   gosnmp.SnmpPDU
		want string
	}{
		{4, pdu([]byte{0x02, 0, 0, 0, 0, 0x0a}), "02:00:00:00:00:0a"},
		{7, pdu([]byte{0x02, 0, 0, 0, 0, 0x0b}), "02:00:00:00:00:0b"}, // six binary bytes are a MAC whatever the subtype
		{4, pdu([]byte("02-00-00-00-00-0C")), "02:00:00:00:00:0c"},    // MAC as text
		{4, pdu([]byte("0:1:2:3:4:5")), "00:01:02:03:04:05"},          // single-digit octets
		{7, pdu([]byte("core-1\x00")), "core-1"},                      // locally assigned text
		{5, pdu([]byte{0xc0, 0xa8, 0, 1, 7}), "c0:a8:00:01:07"},       // binary, not a MAC
	} {
		if got := chassisString(c.sub, c.in); got != c.want {
			t.Errorf("chassisString(%d, %v) = %q, want %q", c.sub, c.in.Value, got, c.want)
		}
	}
}

func TestNamesAndPreferred(t *testing.T) {
	ns := []Neighbor{{SysName: "ap"}, {SysName: ""}, {SysName: "core", Bridge: true}, {SysName: "ap"}}
	if got := Names(ns); got != "ap, ?, core" {
		t.Errorf("Names = %q", got)
	}
	if n, ok := Preferred(ns); !ok || n.SysName != "core" {
		t.Errorf("Preferred = %+v %v", n, ok)
	}
	if n, _ := Preferred(ns[:2]); n.SysName != "ap" {
		t.Errorf("Preferred without bridge = %+v", n)
	}
	if _, ok := Preferred(nil); ok {
		t.Error("Preferred(nil) ok")
	}
}
