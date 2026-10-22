// Package kea reads DHCP leases from ISC Kea via its HTTP(S) management API.
//
// Two deployments are supported:
//
//   - Kea < 3: the Kea Control Agent (kind "agent") forwards commands to the
//     DHCPv4/DHCPv6 servers named in the "service" parameter and answers
//     with a list of results.
//   - Kea >= 3: the DHCPv4 or DHCPv6 server has its own HTTP control socket
//     (kind "dhcp4" or "dhcp6"); commands go there without "service".
//
// Responses are accepted as a list or a single map in both cases. LANLeecher
// only reads: commands outside readOnlyCommands are refused before anything
// is sent. The server certificate is verified during the TLS handshake, so
// credentials never reach a server that is not trusted.
package kea

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Kind of API endpoint.
type Kind string

const (
	// KindAgent is the Kea Control Agent (Kea before 3.0).
	KindAgent Kind = "agent"
	// KindDHCP4 is the HTTP control socket of the DHCPv4 server (Kea 3.0+).
	KindDHCP4 Kind = "dhcp4"
	// KindDHCP6 is the HTTP control socket of the DHCPv6 server (Kea 3.0+).
	KindDHCP6 Kind = "dhcp6"
)

// Kinds in UI order.
var Kinds = []Kind{KindAgent, KindDHCP4, KindDHCP6}

// Valid reports a known kind.
func (k Kind) Valid() bool { return k == KindAgent || k == KindDHCP4 || k == KindDHCP6 }

// readOnlyCommands is the complete list of commands LANLeecher may send.
var readOnlyCommands = map[string]bool{
	"version-get":     true,
	"list-commands":   true,
	"lease4-get-page": true,
	"lease6-get-page": true,
}

// Kea result codes.
const (
	resultSuccess     = 0
	resultError       = 1
	resultUnsupported = 2
	resultEmpty       = 3
)

// Errors.
var (
	ErrAuth         = errors.New("authentication failed (check user name and password)")
	ErrHookMissing  = errors.New("the lease_cmds hook (libdhcp_lease_cmds.so) is not loaded")
	ErrNotReadOnly  = errors.New("command is not on the read-only allowlist")
	ErrInsecureHTTP = errors.New("plain HTTP is only allowed for localhost; use https://")
)

// Config describes one Kea endpoint.
type Config struct {
	URL  string
	Kind Kind
	// DHCP4/DHCP6 select the servers behind a Control Agent.
	DHCP4, DHCP6 bool
	Username     string
	Password     string
	// CAPEM adds trusted certificates (an internal CA or the server's own
	// self-signed certificate) to the system roots.
	CAPEM string
	// ClientCertPEM and ClientKeyPEM enable mutual TLS.
	ClientCertPEM string
	ClientKeyPEM  string
	// PinnedSHA256 accepts exactly this server certificate (hex SHA-256 of
	// the DER encoding) and nothing else.
	PinnedSHA256 string
	// InsecureSkipVerify disables certificate verification. Never the default.
	InsecureSkipVerify bool
	Timeout            time.Duration
	// PageLimit is the number of leases per request (default 1000).
	PageLimit int
}

// CertError reports a server certificate that is not trusted.
type CertError struct {
	// Problem is "untrusted" (no valid chain, hostname mismatch, expired …)
	// or "changed" (differs from the pinned certificate).
	Problem string
	Reason  string
	Cert    *x509.Certificate
	Pinned  string
}

func (e *CertError) Error() string {
	if e.Problem == "changed" {
		return "server certificate changed: pinned " + FormatFingerprint(e.Pinned) + ", got " +
			FormatFingerprint(Fingerprint(e.Cert))
	}
	return "server certificate not trusted: " + e.Reason
}

// Client talks to one Kea endpoint.
type Client struct {
	cfg  Config
	host string
	http *http.Client

	mu        sync.Mutex
	presented []*x509.Certificate
	tlsState  *tls.ConnectionState
}

// ValidateURL checks scheme and host: https, or http for localhost only.
func ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, ErrInsecureHTTP
		}
	default:
		return nil, fmt.Errorf("URL must start with https://")
	}
	return u, nil
}

// New creates a client.
func New(cfg Config) (*Client, error) {
	u, err := ValidateURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	if !cfg.Kind.Valid() {
		return nil, fmt.Errorf("unknown kind %q", cfg.Kind)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.PageLimit <= 0 {
		cfg.PageLimit = 1000
	}
	c := &Client{cfg: cfg, host: u.Hostname()}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Verification happens in verifyConnection, so that pinning and the
		// "trust this certificate" flow work alongside normal chain checks.
		InsecureSkipVerify: true, //nolint:gosec // verified in VerifyConnection
		VerifyConnection:   c.verifyConnection,
	}
	if cfg.ClientCertPEM != "" || cfg.ClientKeyPEM != "" {
		pair, err := tls.X509KeyPair([]byte(cfg.ClientCertPEM), []byte(cfg.ClientKeyPEM))
		if err != nil {
			return nil, fmt.Errorf("client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{pair}
	}
	c.http = &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			TLSClientConfig:     tlsCfg,
			Proxy:               nil, // Kea is in the local network; never send credentials through a proxy
			MaxIdleConns:        2,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: cfg.Timeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c, nil
}

// Close releases idle connections.
func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) verifyConnection(cs tls.ConnectionState) error {
	c.mu.Lock()
	c.presented = cs.PeerCertificates
	c.tlsState = &cs
	c.mu.Unlock()
	if len(cs.PeerCertificates) == 0 {
		return &CertError{Problem: "untrusted", Reason: "no certificate presented"}
	}
	leaf := cs.PeerCertificates[0]
	if c.cfg.InsecureSkipVerify {
		return nil
	}
	if c.cfg.PinnedSHA256 != "" {
		if strings.EqualFold(Fingerprint(leaf), c.cfg.PinnedSHA256) {
			return nil
		}
		return &CertError{Problem: "changed", Cert: leaf, Pinned: c.cfg.PinnedSHA256,
			Reason: "the certificate differs from the trusted one"}
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if c.cfg.CAPEM != "" && !roots.AppendCertsFromPEM([]byte(c.cfg.CAPEM)) {
		return &CertError{Problem: "untrusted", Cert: leaf, Reason: "the stored CA certificate could not be read"}
	}
	inter := x509.NewCertPool()
	for _, ic := range cs.PeerCertificates[1:] {
		inter.AddCert(ic)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, DNSName: c.host}); err != nil {
		return &CertError{Problem: "untrusted", Cert: leaf, Reason: err.Error()}
	}
	return nil
}

// Presented returns the certificate chain of the last TLS handshake.
func (c *Client) Presented() []*x509.Certificate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.presented
}

// TLSVersion returns the negotiated TLS version of the last handshake ("" for HTTP).
func (c *Client) TLSVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tlsState == nil {
		return ""
	}
	return tls.VersionName(c.tlsState.Version)
}

// response is one result of a Kea command.
type response struct {
	Result    int             `json:"result"`
	Text      string          `json:"text"`
	Arguments json.RawMessage `json:"arguments"`
}

// Command sends a read-only command. service is "dhcp4", "dhcp6" or "" (the
// endpoint itself); it is only sent to a Control Agent.
func (c *Client) command(ctx context.Context, command, service string, args any) (*response, error) {
	if !readOnlyCommands[command] {
		return nil, ErrNotReadOnly
	}
	req := map[string]any{"command": command}
	if args != nil {
		req["arguments"] = args
	}
	if c.cfg.Kind == KindAgent && service != "" {
		req["service"] = []string{service}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.Username != "" {
		httpReq.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		var ce *CertError
		if errors.As(err, &ce) {
			return nil, ce
		}
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrAuth
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(firstLine(data)))
	}
	return parseResponse(data)
}

// parseResponse accepts a list of results (Control Agent) or a single map.
func parseResponse(data []byte) (*response, error) {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		var list []response
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("invalid response: %w", err)
		}
		if len(list) == 0 {
			return nil, errors.New("empty response")
		}
		return &list[0], nil
	}
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &r, nil
}

func firstLine(b []byte) string {
	s := string(b)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Families returns the DHCP services this endpoint serves: "dhcp4", "dhcp6".
func (c *Client) Families() []string {
	switch c.cfg.Kind {
	case KindDHCP4:
		return []string{"dhcp4"}
	case KindDHCP6:
		return []string{"dhcp6"}
	}
	var out []string
	if c.cfg.DHCP4 {
		out = append(out, "dhcp4")
	}
	if c.cfg.DHCP6 {
		out = append(out, "dhcp6")
	}
	return out
}

// Version returns the version of the DHCP server (or of the Control Agent if
// no family is selected).
func (c *Client) Version(ctx context.Context) (string, error) {
	service := ""
	if fams := c.Families(); len(fams) > 0 {
		service = fams[0]
	}
	r, err := c.command(ctx, "version-get", service, nil)
	if err != nil {
		return "", err
	}
	if r.Result != resultSuccess {
		return "", fmt.Errorf("version-get: %s", r.Text)
	}
	return r.Text, nil
}

// Lease is an active or historic lease as reported by Kea.
type Lease struct {
	Family    string // dhcp4 or dhcp6
	IP        string `json:"ip-address"`
	HWAddress string `json:"hw-address"`
	Hostname  string `json:"hostname"`
	State     int    `json:"state"`
	ValidLft  int64  `json:"valid-lft"`
	CLTT      int64  `json:"cltt"`
}

// Active reports whether the lease is in the default state and not expired.
func (l Lease) Active(now time.Time) bool {
	if l.State != 0 {
		return false
	}
	if l.ValidLft == 0 || l.CLTT == 0 {
		return true
	}
	return l.CLTT+l.ValidLft > now.Unix()
}

// Leases pages through all leases of a family ("dhcp4" or "dhcp6") with
// lease4-get-page / lease6-get-page and calls fn for each.
func (c *Client) Leases(ctx context.Context, family string, fn func(Lease) error) error {
	cmd := "lease4-get-page"
	if family == "dhcp6" {
		cmd = "lease6-get-page"
	}
	from := "start"
	for {
		r, err := c.command(ctx, cmd, family, map[string]any{"from": from, "limit": c.cfg.PageLimit})
		if err != nil {
			return err
		}
		switch r.Result {
		case resultSuccess:
		case resultEmpty:
			return nil
		case resultUnsupported:
			return ErrHookMissing
		default:
			return fmt.Errorf("%s: %s", cmd, r.Text)
		}
		var args struct {
			Leases []Lease `json:"leases"`
			Count  int     `json:"count"`
		}
		if err := json.Unmarshal(r.Arguments, &args); err != nil {
			return fmt.Errorf("%s: invalid arguments: %w", cmd, err)
		}
		for _, l := range args.Leases {
			l.Family = family
			if err := fn(l); err != nil {
				return err
			}
		}
		if len(args.Leases) < c.cfg.PageLimit || len(args.Leases) == 0 {
			return nil
		}
		from = args.Leases[len(args.Leases)-1].IP
	}
}

// Fingerprint returns the hex SHA-256 of a certificate's DER encoding.
func Fingerprint(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// FormatFingerprint renders a hex fingerprint as AB:CD:… for display.
func FormatFingerprint(fp string) string {
	fp = strings.ToUpper(fp)
	var b strings.Builder
	for i := 0; i+1 < len(fp); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(fp[i : i+2])
	}
	return b.String()
}

// EncodePEM returns the PEM encoding of a certificate.
func EncodePEM(cert *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

// ParsePEM parses the first certificate of a PEM block.
func ParsePEM(s string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM certificate found")
	}
	return x509.ParseCertificate(block.Bytes)
}

// CertInfo is what the UI shows about a certificate.
type CertInfo struct {
	Subject     string
	Issuer      string
	SANs        []string
	NotBefore   time.Time
	NotAfter    time.Time
	SHA256      string
	SelfSigned  bool
	ExpiresSoon bool // within 14 days
	Expired     bool
}

// Info describes a certificate for display.
func Info(cert *x509.Certificate, now time.Time) CertInfo {
	if cert == nil {
		return CertInfo{}
	}
	i := CertInfo{Subject: cert.Subject.String(), Issuer: cert.Issuer.String(), NotBefore: cert.NotBefore,
		NotAfter: cert.NotAfter, SHA256: FormatFingerprint(Fingerprint(cert)),
		SelfSigned: bytes.Equal(cert.RawIssuer, cert.RawSubject)}
	i.SANs = append(i.SANs, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		i.SANs = append(i.SANs, ip.String())
	}
	i.Expired = now.After(cert.NotAfter)
	i.ExpiresSoon = !i.Expired && cert.NotAfter.Sub(now) < 14*24*time.Hour
	return i
}
