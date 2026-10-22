package kea_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/kea"
	"github.com/lan-dot-party/lanleecher/internal/kea/keasim"
)

// start runs a simulator with a fresh self-signed certificate for 127.0.0.1.
func start(t *testing.T, sim *keasim.Server) (*httptest.Server, string) {
	t.Helper()
	pair, cert, err := keasim.SelfSigned("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(sim)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts, kea.Fingerprint(cert)
}

func leases(n int) []keasim.Lease {
	var out []keasim.Lease
	for i := 0; i < n; i++ {
		out = append(out, keasim.Lease{IP: fmt.Sprintf("192.0.2.%d", i+1), HWAddress: fmt.Sprintf("02:00:00:00:00:%02x", i),
			Hostname: fmt.Sprintf("pc-%d", i), ValidLft: 3600, CLTT: time.Now().Unix()})
	}
	return out
}

func collect(t *testing.T, c *kea.Client, family string) []kea.Lease {
	t.Helper()
	var out []kea.Lease
	if err := c.Leases(context.Background(), family, func(l kea.Lease) error { out = append(out, l); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestControlAgentKea2(t *testing.T) {
	sim := &keasim.Server{Mode: "agent", Version: "2.6.3", Username: "lanleecher", Password: "secret-password",
		Leases4: leases(7), Leases6: []keasim.Lease{{IP: "2001:db8::10", HWAddress: "02:00:00:00:00:99", Hostname: "v6-pc"}}}
	ts, fp := start(t, sim)
	c, err := kea.New(kea.Config{URL: ts.URL, Kind: kea.KindAgent, DHCP4: true, DHCP6: true, Username: "lanleecher",
		Password: "secret-password", PinnedSHA256: fp, PageLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c.Version(context.Background()); err != nil || v != "2.6.3" {
		t.Fatalf("Version = %q, %v", v, err)
	}
	if got := collect(t, c, "dhcp4"); len(got) != 7 || got[6].Hostname != "pc-6" {
		t.Fatalf("paging over 3 pages returned %d leases", len(got))
	}
	if got := collect(t, c, "dhcp6"); len(got) != 1 || got[0].Family != "dhcp6" {
		t.Fatalf("dhcp6 leases = %+v", got)
	}
	// Every forwarded command names its service.
	for _, r := range sim.Requests() {
		if _, ok := r["service"]; !ok {
			t.Fatalf("request without service: %v", r)
		}
	}
}

func TestDirectSocketKea3(t *testing.T) {
	sim := &keasim.Server{Mode: "dhcp4", Version: "3.0.1", Leases4: leases(2)}
	ts, fp := start(t, sim)
	c, err := kea.New(kea.Config{URL: ts.URL, Kind: kea.KindDHCP4, PinnedSHA256: fp})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Version(context.Background()); v != "3.0.1" {
		t.Fatalf("Version = %q", v)
	}
	if got := collect(t, c, "dhcp4"); len(got) != 2 {
		t.Fatalf("got %d leases", len(got))
	}
	for _, r := range sim.Requests() {
		if _, ok := r["service"]; ok {
			t.Fatalf("direct socket got a service parameter: %v", r)
		}
	}
}

func TestUntrustedCertificateSendsNothing(t *testing.T) {
	sim := &keasim.Server{Mode: "agent", Version: "2.6.3", Username: "u", Password: "p"}
	ts, _ := start(t, sim)
	c, _ := kea.New(kea.Config{URL: ts.URL, Kind: kea.KindAgent, DHCP4: true, Username: "u", Password: "p"})
	_, err := c.Version(context.Background())
	var ce *kea.CertError
	if !errors.As(err, &ce) || ce.Problem != "untrusted" || ce.Cert == nil {
		t.Fatalf("err = %v, want untrusted certificate", err)
	}
	if len(sim.Requests()) != 0 {
		t.Fatal("a request (with credentials) reached a server whose certificate is not trusted")
	}
	if len(c.Presented()) == 0 {
		t.Fatal("presented certificate not recorded")
	}
}

func TestPinnedCertificateChanged(t *testing.T) {
	sim := &keasim.Server{Mode: "agent", Version: "2.6.3"}
	ts, _ := start(t, sim)
	c, _ := kea.New(kea.Config{URL: ts.URL, Kind: kea.KindAgent, DHCP4: true, PinnedSHA256: "00ff"})
	_, err := c.Version(context.Background())
	var ce *kea.CertError
	if !errors.As(err, &ce) || ce.Problem != "changed" {
		t.Fatalf("err = %v, want changed certificate", err)
	}
}

func TestTrustedCA(t *testing.T) {
	sim := &keasim.Server{Mode: "agent", Version: "2.6.3"}
	pair, cert, _ := keasim.SelfSigned("127.0.0.1")
	ts := httptest.NewUnstartedServer(sim)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	ts.StartTLS()
	defer ts.Close()
	c, _ := kea.New(kea.Config{URL: ts.URL, Kind: kea.KindAgent, DHCP4: true, CAPEM: kea.EncodePEM(cert)})
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatalf("certificate from CAPEM not trusted: %v", err)
	}
}

func TestAuthAndHookErrors(t *testing.T) {
	sim := &keasim.Server{Mode: "agent", Version: "2.6.3", Username: "u", Password: "right", NoLeaseCmds: true}
	ts, fp := start(t, sim)
	bad, _ := kea.New(kea.Config{URL: ts.URL, Kind: kea.KindAgent, DHCP4: true, Username: "u", Password: "wrong", PinnedSHA256: fp})
	if _, err := bad.Version(context.Background()); !errors.Is(err, kea.ErrAuth) {
		t.Fatalf("wrong password: err = %v", err)
	}
	good, _ := kea.New(kea.Config{URL: ts.URL, Kind: kea.KindAgent, DHCP4: true, Username: "u", Password: "right", PinnedSHA256: fp})
	err := good.Leases(context.Background(), "dhcp4", func(kea.Lease) error { return nil })
	if !errors.Is(err, kea.ErrHookMissing) {
		t.Fatalf("missing hook: err = %v", err)
	}
}

func TestURLRules(t *testing.T) {
	for url, ok := range map[string]bool{
		"https://kea.example:8000/": true,
		"http://127.0.0.1:8000/":    true,
		"http://localhost:8000":     true,
		"http://kea.example:8000/":  false,
		"ftp://kea.example/":        false,
	} {
		if _, err := kea.ValidateURL(url); (err == nil) != ok {
			t.Errorf("ValidateURL(%q) err = %v, want ok=%v", url, err, ok)
		}
	}
}

func TestLeaseActive(t *testing.T) {
	now := time.Now()
	cases := []struct {
		l    kea.Lease
		want bool
	}{
		{kea.Lease{State: 0, ValidLft: 3600, CLTT: now.Unix() - 60}, true},
		{kea.Lease{State: 0, ValidLft: 60, CLTT: now.Unix() - 3600}, false}, // expired
		{kea.Lease{State: 1, ValidLft: 3600, CLTT: now.Unix()}, false},      // declined
	}
	for _, c := range cases {
		if c.l.Active(now) != c.want {
			t.Errorf("%+v Active = %v", c.l, !c.want)
		}
	}
}
