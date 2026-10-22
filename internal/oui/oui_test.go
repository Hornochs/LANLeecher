package oui

import "testing"

func TestLookup(t *testing.T) {
	tests := []struct {
		mac, want string
	}{
		{"b8:27:eb:12:34:56", "Raspberry Pi Foundation"},       // MA-L
		{"3C-07-54-AA-BB-CC", "Apple, Inc."},                   // any notation
		{"c8:5c:e2:7a:bc:de", "SYNERGY SYSTEMS AND SOLUTIONS"}, // MA-M (28 bit)
		{"70:b3:d5:06:0a:bc", "RCH SPA"},                       // MA-S (36 bit) inside the IEEE block
		{"70:b3:d5:ff:ff:ff", ""},                              // unassigned part of the IEEE block falls back to MA-L
		{"02:00:00:00:01:03", ""},                              // locally administered: no vendor
		{"01:00:5e:00:00:fb", ""},                              // multicast
		{"not a mac", ""},
	}
	for _, tt := range tests {
		got := LookupString(tt.mac)
		if tt.mac == "70:b3:d5:ff:ff:ff" {
			// Either nothing more specific is assigned (IEEE RA from MA-L) or an MA-S vendor.
			if got == "" {
				t.Errorf("LookupString(%q) = empty, want at least the MA-L owner", tt.mac)
			}
			continue
		}
		if got != tt.want {
			t.Errorf("LookupString(%q) = %q, want %q", tt.mac, got, tt.want)
		}
	}
	if Size() < 50000 {
		t.Fatalf("only %d prefixes loaded", Size())
	}
}
