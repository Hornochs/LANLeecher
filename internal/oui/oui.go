// Package oui maps MAC addresses to the vendor that the IEEE assigned the
// address block to (MA-L, MA-M and MA-S registries).
//
// The data is embedded (oui.txt.gz, built by tools/genoui) and loaded on first
// use. Locally administered addresses – often randomized by phones and
// laptops – belong to no vendor and return "".
package oui

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"strconv"
	"strings"
	"sync"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
)

//go:embed oui.txt.gz
var data []byte

var (
	once sync.Once
	l24  map[uint64]string // 6 hex digits
	l28  map[uint64]string // 7 hex digits
	l36  map[uint64]string // 9 hex digits
)

func load() {
	l24, l28, l36 = map[uint64]string{}, map[uint64]string{}, map[uint64]string{}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return
	}
	defer zr.Close()
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		line := sc.Text()
		prefix, name, ok := strings.Cut(line, "\t")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		v, err := strconv.ParseUint(prefix, 16, 64)
		if err != nil {
			continue
		}
		switch len(prefix) {
		case 6:
			l24[v] = name
		case 7:
			l28[v] = name
		case 9:
			l36[v] = name
		}
	}
}

// Lookup returns the vendor of a MAC address, or "" if unknown or locally
// administered. The longest matching assignment wins.
func Lookup(m macaddr.MAC) string {
	if m.IsLocal() || m.IsMulticast() || m.IsZero() {
		return ""
	}
	once.Do(load)
	v := uint64(m[0])<<40 | uint64(m[1])<<32 | uint64(m[2])<<24 | uint64(m[3])<<16 | uint64(m[4])<<8 | uint64(m[5])
	if name, ok := l36[v>>12]; ok {
		return name
	}
	if name, ok := l28[v>>20]; ok {
		return name
	}
	return l24[v>>24]
}

// LookupString is Lookup for a MAC in any notation.
func LookupString(s string) string {
	m, err := macaddr.Parse(s)
	if err != nil {
		return ""
	}
	return Lookup(m)
}

// Size returns the number of known prefixes (for diagnostics).
func Size() int {
	once.Do(load)
	return len(l24) + len(l28) + len(l36)
}
