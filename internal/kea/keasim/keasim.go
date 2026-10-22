// Package keasim simulates the Kea management API for tests and demos.
//
// Mode "agent" behaves like the Kea Control Agent (Kea < 3): commands for the
// DHCP servers need a "service" list, and every answer is a list with one
// result per service. Modes "dhcp4" and "dhcp6" behave like the HTTP control
// socket of a Kea 3 DHCP server: no "service", a single result map.
package keasim

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Lease is a simulated lease.
type Lease struct {
	IP        string `json:"ip-address"`
	HWAddress string `json:"hw-address"`
	Hostname  string `json:"hostname"`
	State     int    `json:"state"`
	ValidLft  int64  `json:"valid-lft"`
	CLTT      int64  `json:"cltt"`
	SubnetID  int    `json:"subnet-id"`
}

// Server is a simulated Kea endpoint.
type Server struct {
	// Mode is "agent", "dhcp4" or "dhcp6".
	Mode string
	// Version reported by version-get.
	Version string
	// Username/Password enable HTTP basic authentication.
	Username, Password string
	// NoLeaseCmds simulates a server without the lease_cmds hook.
	NoLeaseCmds bool
	Leases4     []Lease
	Leases6     []Lease

	mu       sync.Mutex
	requests []map[string]any
}

// Requests returns the commands received so far (for tests).
func (s *Server) Requests() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.requests...)
}

type result struct {
	Result    int    `json:"result"`
	Text      string `json:"text"`
	Arguments any    `json:"arguments,omitempty"`
}

// ServeHTTP implements the management API.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Username != "" {
		u, p, ok := r.BasicAuth()
		if !ok || u != s.Username || p != s.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="kea"`)
			http.Error(w, `{ "result": 401, "text": "Unauthorized" }`, http.StatusUnauthorized)
			return
		}
	}
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, []result{{Result: 1, Text: "invalid JSON"}})
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	cmd, _ := req["command"].(string)
	args, _ := req["arguments"].(map[string]any)

	if s.Mode != "agent" {
		writeJSON(w, s.handle(s.Mode, cmd, args))
		return
	}
	services, _ := req["service"].([]any)
	if len(services) == 0 {
		// Commands for the Control Agent itself.
		switch cmd {
		case "version-get":
			writeJSON(w, []result{{Result: 0, Text: s.Version}})
		case "list-commands":
			writeJSON(w, []result{{Result: 0, Text: "list of commands", Arguments: []string{"list-commands", "version-get", "config-get"}}})
		default:
			writeJSON(w, []result{{Result: 2, Text: "'" + cmd + "' command not supported."}})
		}
		return
	}
	var out []result
	for _, svc := range services {
		name, _ := svc.(string)
		out = append(out, s.handle(name, cmd, args))
	}
	writeJSON(w, out)
}

func (s *Server) handle(service, cmd string, args map[string]any) result {
	if service != "dhcp4" && service != "dhcp6" {
		return result{Result: 1, Text: "forwarding socket is not configured for the server type " + service}
	}
	switch cmd {
	case "version-get":
		return result{Result: 0, Text: s.Version}
	case "list-commands":
		cmds := []string{"list-commands", "version-get", "config-get"}
		if !s.NoLeaseCmds {
			cmds = append(cmds, "lease4-get-page", "lease6-get-page")
		}
		return result{Result: 0, Text: "list of commands", Arguments: cmds}
	case "lease4-get-page", "lease6-get-page":
		if s.NoLeaseCmds {
			return result{Result: 2, Text: "'" + cmd + "' command not supported."}
		}
		leases := s.Leases4
		if cmd == "lease6-get-page" {
			leases = s.Leases6
		}
		if (cmd == "lease4-get-page") != (service == "dhcp4") {
			return result{Result: 2, Text: "'" + cmd + "' command not supported."}
		}
		return page(leases, args)
	}
	return result{Result: 2, Text: "'" + cmd + "' command not supported."}
}

func page(leases []Lease, args map[string]any) result {
	sorted := append([]Lease(nil), leases...)
	sort.Slice(sorted, func(i, j int) bool {
		a, _ := netip.ParseAddr(sorted[i].IP)
		b, _ := netip.ParseAddr(sorted[j].IP)
		return a.Less(b)
	})
	from, _ := args["from"].(string)
	limit := 1000
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	start := 0
	if from != "" && from != "start" {
		fromIP, err := netip.ParseAddr(from)
		if err != nil {
			return result{Result: 1, Text: "invalid from address"}
		}
		start = len(sorted)
		for i, l := range sorted {
			if ip, _ := netip.ParseAddr(l.IP); fromIP.Less(ip) {
				start = i
				break
			}
		}
	}
	end := min(start+limit, len(sorted))
	if start >= end {
		return result{Result: 3, Text: "0 lease(s) found."}
	}
	chunk := sorted[start:end]
	return result{Result: 0, Text: fmt.Sprintf("%d lease(s) found.", len(chunk)),
		Arguments: map[string]any{"leases": chunk, "count": len(chunk)}}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// SelfSigned returns a fresh self-signed certificate for the given hosts.
func SelfSigned(hosts ...string) (tls.Certificate, *x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "kea-sim", Organization: []string{"LANLeecher test"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true, BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}, cert, nil
}

// DemoLeases returns leases for the demo switch (testdata/snmpsim/demo.snmprec).
func DemoLeases() []Lease {
	names := []string{"fragmaster-pc", "desktop-k7q2m", "nightowl", "lan-rig-02", "pixelpirate", "desktop-zr41x",
		"headshot-hannes", "mobile-station", "coffee-machine", "desktop-3vb9p", "awp-only", "retro-box",
		"teamspeak-tim", "desktop-lq8rt", "rocketjump", "laptop-lea", "noscope-nils", "pc-corner", "streaming-pc",
		"desktop-m4xw1"}
	now := time.Now().Unix()
	var out []Lease
	for i, n := range names {
		out = append(out, Lease{IP: fmt.Sprintf("192.0.2.%d", 101+i), HWAddress: fmt.Sprintf("02:00:00:00:01:%02x", i+1),
			Hostname: n + ".lan.example.", ValidLft: 3600, CLTT: now - 600, SubnetID: 1})
	}
	return out
}
