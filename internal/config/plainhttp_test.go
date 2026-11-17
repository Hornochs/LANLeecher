package config

import "testing"

func TestPlainHTTPOnNetwork(t *testing.T) {
	for _, tt := range []struct {
		listen, public, cert string
		want                 bool
	}{
		{"127.0.0.1:9040", "http://127.0.0.1:9040", "", false},
		{"[::1]:9040", "http://localhost:9040", "", false},
		{"localhost:9040", "http://localhost:9040", "", false},
		{"0.0.0.0:9040", "http://localhost:9040", "", true},
		{":9040", "http://lanleecher.lan", "", true},
		{"10.0.0.5:9040", "http://10.0.0.5:9040", "", true},
		{"0.0.0.0:9040", "https://lanleecher.example", "", false},
		{"0.0.0.0:9040", "http://lanleecher.lan", "/etc/lanleecher/cert.pem", false},
	} {
		c := &Config{Listen: tt.listen, PublicURL: tt.public, TLS: TLS{CertFile: tt.cert}}
		if got := c.PlainHTTPOnNetwork(); got != tt.want {
			t.Errorf("%s %s cert=%q: got %v, want %v", tt.listen, tt.public, tt.cert, got, tt.want)
		}
	}
}
