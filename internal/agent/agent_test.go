package agent

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lan-dot-party/lanleecher/internal/tlsclient"
)

const testToken = "llk_0123456789abcdefghijABCDEFGHIJ0123456789"

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agent.conf")
	if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigValidation(t *testing.T) {
	cases := map[string]string{
		"http without opt-in": "server: http://lanleecher.example\ntoken: " + testToken + "\n",
		"ftp":                 "server: ftp://lanleecher.example\ntoken: " + testToken + "\n",
		"no token":            "server: https://lanleecher.example\n",
		"bad token":           "server: https://lanleecher.example\ntoken: secret\n",
		"bad mode":            "server: https://lanleecher.example\ntoken: " + testToken + "\nmode: windows\n",
		"short interval":      "server: https://lanleecher.example\ntoken: " + testToken + "\ninterval: 1s\n",
		"unknown key":         "server: https://lanleecher.example\ntoken: " + testToken + "\nservr: x\n",
		"pin and insecure":    "server: https://lanleecher.example\ntoken: " + testToken + "\ntls:\n  pinned_sha256: ab\n  insecure_skip_verify: true\n",
		"credentials in URL":  "server: https://user:pw@lanleecher.example\ntoken: " + testToken + "\n",
	}
	for name, body := range cases {
		if _, _, err := LoadConfig(writeConfig(t, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	cfg, warnings, err := LoadConfig(writeConfig(t, "server: https://lanleecher.example/sub/\ntoken: "+testToken+"\n"))
	if err != nil || len(warnings) != 0 {
		t.Fatalf("valid config: %v %v", err, warnings)
	}
	if cfg.Endpoint() != "https://lanleecher.example/sub/api/v1/agent/report" || cfg.Interval.Seconds() != 15 || cfg.Mode != "auto" {
		t.Fatalf("defaults: %+v", cfg)
	}
	_, warnings, err = LoadConfig(writeConfig(t, "server: http://127.0.0.1:9\ntoken: "+testToken+"\nallow_insecure_http: true\n"))
	if err != nil || len(warnings) != 1 {
		t.Fatalf("http opt-in must work with a warning: %v %v", err, warnings)
	}
}

func newTestAgent(t *testing.T, cfg *Config) *Agent {
	t.Helper()
	if _, err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestTLSVerification(t *testing.T) {
	var gotAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var rep Report
		if r.URL.Path != "/api/v1/agent/report" || json.NewDecoder(r.Body).Decode(&rep) != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(Response{Accepted: len(rep.Interfaces)})
	}))
	defer srv.Close()
	pin := tlsclient.Fingerprint(srv.Certificate())
	report := Report{Hostname: "h", Mode: "linux", Interfaces: []ReportInterface{{MAC: "02:00:00:00:00:01", IfName: "eth0", Kind: "host"}}}

	// Self-signed, no pin: rejected (the default trusts only valid certificates).
	a := newTestAgent(t, &Config{Server: srv.URL, Token: testToken})
	_, err := a.Send(context.Background(), report)
	var ce *tlsclient.CertError
	if !errors.As(err, &ce) || ce.Problem != "untrusted" {
		t.Fatalf("self-signed without pin: %v", err)
	}
	if gotAuth != "" {
		t.Fatal("token sent to an untrusted server")
	}

	// Pinned (with colons): accepted, token sent.
	cfg := &Config{Server: srv.URL, Token: testToken}
	cfg.TLS.PinnedSHA256 = tlsclient.FormatFingerprint(pin)
	res, err := newTestAgent(t, cfg).Send(context.Background(), report)
	if err != nil || res.Accepted != 1 || gotAuth != "Bearer "+testToken {
		t.Fatalf("pinned: %v %+v %q", err, res, gotAuth)
	}

	// Wrong pin: rejected as changed.
	cfg = &Config{Server: srv.URL, Token: testToken}
	cfg.TLS.PinnedSHA256 = strings.Repeat("ab", 32)
	if _, err := newTestAgent(t, cfg).Send(context.Background(), report); !errors.As(err, &ce) || ce.Problem != "changed" {
		t.Fatalf("wrong pin: %v", err)
	}

	// The server's certificate as CA file: accepted (valid for 127.0.0.1).
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, []byte(encodePEM(srv.Certificate().Raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = &Config{Server: srv.URL, Token: testToken}
	cfg.TLS.CAFile = caFile
	if _, err := newTestAgent(t, cfg).Send(context.Background(), report); err != nil {
		t.Fatalf("ca_file: %v", err)
	}

	// insecure_skip_verify: accepted.
	cfg = &Config{Server: srv.URL, Token: testToken}
	cfg.TLS.InsecureSkipVerify = true
	if _, err := newTestAgent(t, cfg).Send(context.Background(), report); err != nil {
		t.Fatalf("insecure: %v", err)
	}
}

func TestNoRedirectsAndStatusErrors(t *testing.T) {
	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+testToken+"x" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":"invalid_token","detail":"token unknown, revoked or expired"}`)
			return
		}
		http.Redirect(w, r, target.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	a := newTestAgent(t, &Config{Server: srv.URL, Token: testToken, AllowInsecureHTTP: true})
	var se *StatusError
	if _, err := a.Send(context.Background(), Report{}); !errors.As(err, &se) || se.Status != http.StatusTemporaryRedirect || hits != 0 {
		t.Fatalf("redirect: %v, hits %d", err, hits)
	}
	a = newTestAgent(t, &Config{Server: srv.URL, Token: testToken + "x", AllowInsecureHTTP: true})
	if _, err := a.Send(context.Background(), Report{}); !errors.As(err, &se) || se.Status != 401 ||
		!strings.Contains(err.Error(), "revoked") {
		t.Fatalf("401: %v", err)
	}
}

func encodePEM(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
