package app

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/lan-dot-party/lanleecher/internal/config"
)

func TestClientIP(t *testing.T) {
	cfg := config.Default()
	cfg.TrustedProxies = []string{"127.0.0.1", "::1", "10.0.0.0/8"}
	a := New(cfg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tt := range []struct{ name, remote, xff, realIP, want string }{
		{"direct client ignores headers", "203.0.113.5:1000", "198.51.100.1", "198.51.100.2", "203.0.113.5"},
		{"nginx: rightmost XFF entry", "127.0.0.1:1000", "192.0.2.9", "192.0.2.9", "192.0.2.9"},
		{"client-sent XFF stays on the left", "127.0.0.1:1000", "198.51.100.1, 192.0.2.9", "", "192.0.2.9"},
		{"spoofed X-Real-IP loses to XFF", "127.0.0.1:1000", "192.0.2.9", "198.51.100.2", "192.0.2.9"},
		{"chain of trusted proxies", "127.0.0.1:1000", "192.0.2.9, 10.1.2.3", "", "192.0.2.9"},
		{"X-Real-IP fallback", "[::1]:1000", "", "192.0.2.9", "192.0.2.9"},
		{"garbage XFF falls back to peer", "127.0.0.1:1000", "not-an-ip", "", "127.0.0.1"},
		{"garbage X-Real-IP falls back to peer", "127.0.0.1:1000", "", "x", "127.0.0.1"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tt.remote
		if tt.xff != "" {
			r.Header.Set("X-Forwarded-For", tt.xff)
		}
		if tt.realIP != "" {
			r.Header.Set("X-Real-IP", tt.realIP)
		}
		if got := a.ClientIP(r); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
