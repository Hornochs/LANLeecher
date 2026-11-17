// Package tlsclient verifies server certificates for LANLeecher's outgoing
// connections (Kea DHCP sources, the host agent): the normal chain and
// hostname check against the system roots plus an optional extra CA, or a
// pinned certificate, or – only when explicitly configured – nothing.
package tlsclient

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"strings"
)

// Options select how a server certificate is checked.
type Options struct {
	// Host is the name the certificate must be valid for.
	Host string
	// CAPEM adds trusted certificates (an internal CA or the server's own
	// self-signed certificate) to the system roots.
	CAPEM string
	// PinnedSHA256 accepts exactly this certificate (hex SHA-256 of the DER
	// encoding, colons and case ignored) and nothing else.
	PinnedSHA256 string
	// InsecureSkipVerify accepts any certificate. Never the default.
	InsecureSkipVerify bool
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

// NormalizeFingerprint removes colons and spaces and lowercases a hex
// fingerprint.
func NormalizeFingerprint(fp string) string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(strings.TrimSpace(fp)))
}

// Verify checks the peer certificates of a TLS handshake.
func Verify(o Options, cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return &CertError{Problem: "untrusted", Reason: "no certificate presented"}
	}
	leaf := cs.PeerCertificates[0]
	if o.InsecureSkipVerify {
		return nil
	}
	if pin := NormalizeFingerprint(o.PinnedSHA256); pin != "" {
		if Fingerprint(leaf) == pin {
			return nil
		}
		return &CertError{Problem: "changed", Cert: leaf, Pinned: pin, Reason: "the certificate differs from the trusted one"}
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if o.CAPEM != "" && !roots.AppendCertsFromPEM([]byte(o.CAPEM)) {
		return &CertError{Problem: "untrusted", Cert: leaf, Reason: "the configured CA certificate could not be read"}
	}
	inter := x509.NewCertPool()
	for _, ic := range cs.PeerCertificates[1:] {
		inter.AddCert(ic)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, DNSName: o.Host}); err != nil {
		return &CertError{Problem: "untrusted", Cert: leaf, Reason: err.Error()}
	}
	return nil
}

// Config returns a TLS configuration that verifies with Verify. observe, if
// set, sees every handshake before verification (to show the presented
// certificate when it is rejected).
func Config(o Options, observe func(tls.ConnectionState)) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Verification happens in VerifyConnection, so that pinning works
		// alongside the normal chain and hostname check.
		InsecureSkipVerify: true, //nolint:gosec // verified in VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if observe != nil {
				observe(cs)
			}
			return Verify(o, cs)
		},
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
