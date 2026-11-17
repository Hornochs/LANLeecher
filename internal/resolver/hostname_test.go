package resolver

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanHostnameNormalises(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain", "gamer-pc", "gamer-pc"},
		{"trailing dot and padding", "  gamer-pc.lan.  \x00\x00", "gamer-pc.lan"},
		{"nul padding", "PC\x00\x00 \x00", "PC"},
		{"control chars", "a\x01b\x7fc\u0085d\te", "abcde"},
		{"invalid utf8", "ab\xffcd\xc3", "abcd"},
		{"bidi override", "evil\u202etxt.exe", "eviltxt.exe"},
		{"bidi isolates", "a\u2066b\u2069c", "abc"},
		{"marks", "a\u200eb\u200fc\u061cd", "abcd"},
		{"zero width", "a\u200bb\u200cc\u200dd\u2060e\ufeff f\u00adg", "abcde fg"},
		{"umlauts kept", "Büro-Rechner-ß", "Büro-Rechner-ß"},
		{"only invisible", "\u200b\u202e", ""},
	}
	for _, tt := range tests {
		if got := cleanHostname(tt.in); got != tt.want {
			t.Errorf("%s: cleanHostname(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestCleanHostnameCutsByRunes(t *testing.T) {
	got := cleanHostname(strings.Repeat("ä", 300))
	if n := utf8.RuneCountInString(got); n != maxHostnameRunes || !utf8.ValidString(got) {
		t.Fatalf("got %d runes (valid=%v), want %d valid runes", n, utf8.ValidString(got), maxHostnameRunes)
	}
	if got := cleanHostname(strings.Repeat("a", 300)); len(got) != maxHostnameRunes {
		t.Fatalf("ascii cut to %d, want %d", len(got), maxHostnameRunes)
	}
}
