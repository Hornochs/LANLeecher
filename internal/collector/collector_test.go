package collector

import "testing"

func TestInterfaceClassification(t *testing.T) {
	tests := []struct {
		name     string
		ifType   int
		physical bool
		lag      bool
	}{
		{"gigabitEthernet 1/0/1", 6, true, false},
		{"two-gigabitEthernet 1/0/3", 6, true, false},
		{"Gi1/0/12", 117, true, false},
		// TP-Link reports ifType 6 for these.
		{"Vlan-interface100", 6, false, false},
		{"port-channel 1", 6, true, true},
		{"Po1", 6, true, true},
		{"ae0", 6, true, true},
		{"bond0", 6, true, true},
		{"Loopback0", 24, false, false},
		{"Vlan1", 136, false, false},
		{"Port-channel2", 161, true, true},
	}
	for _, tt := range tests {
		i := &Interface{Name: tt.name, Type: tt.ifType}
		if i.Physical() != tt.physical || i.IsLAG() != tt.lag {
			t.Errorf("%q type %d: physical=%v lag=%v, want %v %v", tt.name, tt.ifType, i.Physical(), i.IsLAG(), tt.physical, tt.lag)
		}
	}
}
