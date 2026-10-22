package board

import "testing"

func TestPublicNameHidesMACs(t *testing.T) {
	tests := []struct {
		typ, name, want string
	}{
		{"mac", "02:00:00:00:00:07", "Client #7"},
		{"mac", "02-00-00-00-00-07", "Client #7"},
		{"mac", "DESKTOP-FRAG42", "DESKTOP-FRAG42"},
		{"port_group", "Table 7", "Table 7"},
	}
	for _, tt := range tests {
		if got := PublicName(tt.typ, 7, tt.name); got != tt.want {
			t.Errorf("PublicName(%q, %q) = %q, want %q", tt.typ, tt.name, got, tt.want)
		}
	}
}
