package resolver

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func TestNBSTATRoundTrip(t *testing.T) {
	req := nbstatRequest(0x1234)
	if len(req) != 50 {
		t.Fatalf("request length = %d, want 50", len(req))
	}
	if binary.BigEndian.Uint16(req) != 0x1234 {
		t.Fatal("transaction id not set")
	}

	// Build a response with a group name first and the workstation name second.
	resp := append([]byte{}, req[:12]...)
	resp[2] = 0x84
	binary.BigEndian.PutUint16(resp[4:], 0) // qdcount
	binary.BigEndian.PutUint16(resp[6:], 1) // ancount
	resp = append(resp, req[12:46]...)      // name
	resp = append(resp, 0x00, 0x21, 0x00, 0x01, 0, 0, 0, 0)
	entry := func(name string, suffix byte, group bool) []byte {
		e := make([]byte, 18)
		copy(e, []byte(name + "               ")[:15])
		e[15] = suffix
		if group {
			e[16] = 0x80
		}
		return e
	}
	rdata := []byte{2}
	rdata = append(rdata, entry("WORKGROUP", 0x00, true)...)
	rdata = append(rdata, entry("DESKTOP-FRAG42", 0x00, false)...)
	resp = binary.BigEndian.AppendUint16(resp, uint16(len(rdata)))
	resp = append(resp, rdata...)

	name, err := parseNBSTAT(resp)
	if err != nil || name != "DESKTOP-FRAG42" {
		t.Fatalf("parseNBSTAT = %q, %v", name, err)
	}
}

func TestReverseName(t *testing.T) {
	got, _ := reverseName(netip.MustParseAddr("192.0.2.10"))
	if got != "10.2.0.192.in-addr.arpa." {
		t.Fatalf("reverseName = %q", got)
	}
}

func TestCleanHostname(t *testing.T) {
	if got := cleanHostname(" gamie-pc.local.\x00"); got != "gamie-pc.local" {
		t.Fatalf("cleanHostname = %q", got)
	}
}
