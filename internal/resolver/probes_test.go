package resolver

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func ptrResponse(t *testing.T, id uint16, qname, target string) []byte {
	t.Helper()
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{ID: id, Response: true},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(qname), Type: dnsmessage.TypePTR,
				Class: dnsmessage.ClassINET, TTL: 10},
			Body: &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(target)},
		}},
	}
	b, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParsePTRAnswer(t *testing.T) {
	const q = "10.2.0.192.in-addr.arpa."
	tests := []struct {
		name    string
		resp    []byte
		id      uint16
		checkID bool
		want    string
		ok      bool
	}{
		{"llmnr matching id", ptrResponse(t, 0x1234, q, "host.local."), 0x1234, true, "host.local.", true},
		{"llmnr wrong id", ptrResponse(t, 0x1235, q, "host.local."), 0x1234, true, "", false},
		{"mdns id ignored", ptrResponse(t, 0, q, "host.local."), 0x1234, false, "host.local.", true},
		{"other name", ptrResponse(t, 0x1234, "11.2.0.192.in-addr.arpa.", "x.local."), 0x1234, true, "", false},
		{"garbage", []byte{1, 2, 3}, 0x1234, true, "", false},
	}
	for _, tt := range tests {
		got, ok := parsePTRAnswer(tt.resp, q, tt.id, tt.checkID)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSameHost(t *testing.T) {
	ip := netip.MustParseAddr("192.0.2.10")
	tests := []struct {
		a, b netip.Addr
		want bool
	}{
		{ip, ip, true},
		{netip.MustParseAddr("::ffff:192.0.2.10"), ip, true},
		{netip.MustParseAddr("192.0.2.11"), ip, false},
		{netip.MustParseAddr("fe80::1%eth0"), netip.MustParseAddr("fe80::1"), true},
		{netip.MustParseAddr("fe80::2"), netip.MustParseAddr("fe80::1"), false},
	}
	for _, tt := range tests {
		if got := sameHost(tt.a, tt.b); got != tt.want {
			t.Errorf("sameHost(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// A packet from another address must be ignored even if its content is a
// perfectly valid answer; the exchange runs over loopback only.
func TestUDPExchangeIgnoresWrongSource(t *testing.T) {
	host, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skip("loopback sockets unavailable:", err)
	}
	defer host.Close()
	spoofer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 2)})
	if err != nil {
		t.Skip("second loopback address unavailable:", err)
	}
	defer spoofer.Close()
	go func() {
		buf := make([]byte, 64)
		_, client, err := host.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		_, _ = spoofer.WriteToUDPAddrPort([]byte("spoofed"), client)
		time.Sleep(50 * time.Millisecond)
		_, _ = host.WriteToUDPAddrPort([]byte("genuine"), client)
	}()
	target := host.LocalAddr().(*net.UDPAddr).AddrPort()
	got, err := udpExchange(context.Background(), target, []byte("q"), 2*time.Second, func([]byte) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "genuine" {
		t.Fatalf("accepted %q, want the answer of the probed host", got)
	}
}
