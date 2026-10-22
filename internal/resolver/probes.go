package resolver

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// NetBIOS sends a Node Status Query (NBSTAT) to UDP 137 of the client and
// returns its computer name. Windows answers unless the firewall profile is
// "Public".
type NetBIOS struct{ Timeout time.Duration }

// Name implements Prober.
func (NetBIOS) Name() string { return "netbios" }

// Probe implements Prober.
func (n NetBIOS) Probe(ctx context.Context, ip netip.Addr) (string, error) {
	if !ip.Is4() {
		return "", nil
	}
	txid := randomID()
	resp, err := udpExchange(ctx, netip.AddrPortFrom(ip, 137), nbstatRequest(txid), n.Timeout, func(b []byte) bool {
		return len(b) >= 2 && binary.BigEndian.Uint16(b) == txid
	})
	if err != nil {
		return "", err
	}
	return parseNBSTAT(resp)
}

// nbstatRequest builds a node status request for the wildcard name "*".
func nbstatRequest(txid uint16) []byte {
	b := make([]byte, 0, 50)
	b = binary.BigEndian.AppendUint16(b, txid)
	b = append(b, 0x00, 0x00) // flags: query
	b = append(b, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00)
	name := [16]byte{'*'}
	b = append(b, 0x20)
	for _, c := range name {
		b = append(b, 'A'+(c>>4), 'A'+(c&0x0f))
	}
	b = append(b, 0x00)
	b = append(b, 0x00, 0x21, 0x00, 0x01) // type NBSTAT, class IN
	return b
}

// parseNBSTAT returns the first unique workstation name (suffix 0x00).
func parseNBSTAT(b []byte) (string, error) {
	if len(b) < 12 || binary.BigEndian.Uint16(b[6:]) == 0 {
		return "", errors.New("netbios: no answer")
	}
	off := 12
	// Skip the answer name (labels or a compression pointer).
	for off < len(b) {
		l := int(b[off])
		if l == 0 {
			off++
			break
		}
		if l&0xc0 == 0xc0 {
			off += 2
			break
		}
		off += 1 + l
	}
	off += 10 // type, class, ttl, rdlength
	if off >= len(b) {
		return "", errors.New("netbios: short answer")
	}
	count := int(b[off])
	off++
	var fallback string
	for i := 0; i < count && off+18 <= len(b); i++ {
		entry := b[off : off+18]
		off += 18
		name := strings.TrimRight(string(entry[:15]), " \x00")
		suffix := entry[15]
		group := binary.BigEndian.Uint16(entry[16:])&0x8000 != 0
		if suffix == 0x00 && !group && name != "" {
			return name, nil
		}
		if fallback == "" && !group && name != "" {
			fallback = name
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", errors.New("netbios: no name")
}

// MDNS sends a reverse lookup (PTR <ip>.in-addr.arpa) to the client on UDP
// 5353 by unicast and to the mDNS group by multicast. Responders answer
// "legacy unicast" queries directly to our ephemeral port, so no root rights
// are needed. Multicast only reaches the host's own segment.
type MDNS struct{ Timeout time.Duration }

// Name implements Prober.
func (MDNS) Name() string { return "mdns" }

// Probe implements Prober.
func (m MDNS) Probe(ctx context.Context, ip netip.Addr) (string, error) {
	targets := []netip.AddrPort{netip.AddrPortFrom(ip, 5353)}
	if ip.Is4() {
		targets = append(targets, netip.MustParseAddrPort("224.0.0.251:5353"))
	}
	return ptrQuery(ctx, ip, targets, m.Timeout, false)
}

// LLMNR sends a reverse lookup to UDP 5355 of the client, a fallback for
// Windows clients without NetBIOS.
type LLMNR struct{ Timeout time.Duration }

// Name implements Prober.
func (LLMNR) Name() string { return "llmnr" }

// Probe implements Prober.
func (l LLMNR) Probe(ctx context.Context, ip netip.Addr) (string, error) {
	return ptrQuery(ctx, ip, []netip.AddrPort{netip.AddrPortFrom(ip, 5355)}, l.Timeout, true)
}

// DNSPTR uses the system resolver for reverse DNS.
type DNSPTR struct{ Timeout time.Duration }

// Name implements Prober.
func (DNSPTR) Name() string { return "dns-ptr" }

// Probe implements Prober.
func (d DNSPTR) Probe(ctx context.Context, ip netip.Addr) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip.String())
	if err != nil || len(names) == 0 {
		return "", err
	}
	return names[0], nil
}

func ptrQuery(ctx context.Context, ip netip.Addr, targets []netip.AddrPort, timeout time.Duration, llmnr bool) (string, error) {
	qname, err := reverseName(ip)
	if err != nil {
		return "", err
	}
	id := randomID()
	if !llmnr {
		id = 0 // mDNS ignores the ID, but legacy unicast echoes it
	}
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{ID: id},
		Questions: []dnsmessage.Question{{
			Name:  dnsmessage.MustNewName(qname),
			Type:  dnsmessage.TypePTR,
			Class: dnsmessage.ClassINET,
		}},
	}
	query, err := msg.Pack()
	if err != nil {
		return "", err
	}
	var answer string
	_, err = udpExchangeMulti(ctx, targets, query, timeout, func(b []byte) bool {
		var p dnsmessage.Parser
		h, err := p.Start(b)
		if err != nil || !h.Response {
			return false
		}
		if err := p.SkipAllQuestions(); err != nil {
			return false
		}
		for {
			rh, err := p.AnswerHeader()
			if err != nil {
				return false
			}
			if rh.Type != dnsmessage.TypePTR || !strings.EqualFold(rh.Name.String(), qname) {
				if err := p.SkipAnswer(); err != nil {
					return false
				}
				continue
			}
			ptr, err := p.PTRResource()
			if err != nil {
				return false
			}
			answer = ptr.PTR.String()
			return true
		}
	})
	if err != nil {
		return "", err
	}
	return answer, nil
}

func reverseName(ip netip.Addr) (string, error) {
	if ip.Is4() {
		b := ip.As4()
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa.", b[3], b[2], b[1], b[0]), nil
	}
	if ip.Is6() {
		b := ip.As16()
		var sb strings.Builder
		const hex = "0123456789abcdef"
		for i := len(b) - 1; i >= 0; i-- {
			sb.WriteByte(hex[b[i]&0x0f])
			sb.WriteByte('.')
			sb.WriteByte(hex[b[i]>>4])
			sb.WriteByte('.')
		}
		sb.WriteString("ip6.arpa.")
		return sb.String(), nil
	}
	return "", errors.New("invalid ip")
}

func udpExchange(ctx context.Context, target netip.AddrPort, req []byte, timeout time.Duration, accept func([]byte) bool) ([]byte, error) {
	return udpExchangeMulti(ctx, []netip.AddrPort{target}, req, timeout, accept)
}

// udpExchangeMulti sends req to all targets from one unprivileged socket and
// returns the first response accepted by accept.
func udpExchangeMulti(ctx context.Context, targets []netip.AddrPort, req []byte, timeout time.Duration,
	accept func([]byte) bool) ([]byte, error) {
	if timeout <= 0 {
		timeout = time.Second
	}
	network := "udp4"
	if targets[0].Addr().Is6() {
		network = "udp6"
	}
	conn, err := net.ListenUDP(network, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	for _, t := range targets {
		if _, err := conn.WriteToUDPAddrPort(req, t); err != nil && len(targets) == 1 {
			return nil, err
		}
	}
	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return nil, err
		}
		if accept(buf[:n]) {
			return append([]byte(nil), buf[:n]...), nil
		}
	}
}

func randomID() uint16 {
	var b [2]byte
	rand.Read(b[:])
	return binary.BigEndian.Uint16(b[:])
}
