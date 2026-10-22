// Package macaddr parses and normalizes MAC addresses.
//
// Every MAC that enters LANLeecher passes through Parse or FromBytes, so
// the database and API only ever see the form aa:bb:cc:dd:ee:ff.
package macaddr

import (
	"encoding/hex"
	"errors"
	"strings"
)

// MAC is a 48-bit hardware address.
type MAC [6]byte

// ErrInvalid is returned for input that is not a 48-bit MAC.
var ErrInvalid = errors.New("invalid MAC address")

// Parse accepts aa:bb:cc:dd:ee:ff, aa-bb-cc-dd-ee-ff, aabb.ccdd.eeff and
// aabbccddeeff in any letter case.
func Parse(s string) (MAC, error) {
	var m MAC
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ':', '-', '.', ' ':
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(clean) != 12 {
		return m, ErrInvalid
	}
	b, err := hex.DecodeString(clean)
	if err != nil {
		return m, ErrInvalid
	}
	copy(m[:], b)
	return m, nil
}

// FromBytes converts a 6-byte slice (e.g. an SNMP OctetString).
func FromBytes(b []byte) (MAC, bool) {
	var m MAC
	if len(b) != 6 {
		return m, false
	}
	copy(m[:], b)
	return m, true
}

// FromOID converts the last six sub-identifiers of an OID index.
func FromOID(parts []int) (MAC, bool) {
	var m MAC
	if len(parts) != 6 {
		return m, false
	}
	for i, p := range parts {
		if p < 0 || p > 255 {
			return m, false
		}
		m[i] = byte(p)
	}
	return m, true
}

// String returns the normalized form aa:bb:cc:dd:ee:ff.
func (m MAC) String() string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 17)
	for i, b := range m {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, digits[b>>4], digits[b&0x0f])
	}
	return string(out)
}

// IsLocal reports a locally administered (often randomized) address.
func (m MAC) IsLocal() bool { return m[0]&0x02 != 0 }

// IsMulticast reports a group address, which never belongs to a client.
func (m MAC) IsMulticast() bool { return m[0]&0x01 != 0 }

// IsZero reports 00:00:00:00:00:00.
func (m MAC) IsZero() bool { return m == MAC{} }

// Normalize parses s and returns the normalized string, or "" if invalid.
func Normalize(s string) string {
	m, err := Parse(s)
	if err != nil {
		return ""
	}
	return m.String()
}

// LooksLikePartial reports whether s could be part of a MAC (for search).
// It returns the hex digits in normalized order without separators.
func LooksLikePartial(s string) (string, bool) {
	clean := strings.ToLower(strings.Map(func(r rune) rune {
		switch r {
		case ':', '-', '.':
			return -1
		}
		return r
	}, strings.TrimSpace(s)))
	if len(clean) < 4 || len(clean) > 12 {
		return "", false
	}
	if _, err := hex.DecodeString(clean + strings.Repeat("0", len(clean)%2)); err != nil {
		return "", false
	}
	return clean, true
}
