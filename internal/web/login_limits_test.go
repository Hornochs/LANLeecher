package web

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/lan-dot-party/lanleecher/internal/auth"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

var reLoginCSRF = regexp.MustCompile(`name="_login_csrf" value="([0-9a-f]+)"`)

// tryLogin logs in with a fresh cookie jar; clientIP goes into
// X-Forwarded-For (the test server's peer 127.0.0.1 is a trusted proxy).
func tryLogin(t *testing.T, h *harness, name, password, clientIP string) *http.Response {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(h.srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := reLoginCSRF.FindStringSubmatch(string(body))
	if m == nil {
		t.Fatal("no login CSRF token")
	}
	form := url.Values{"username": {name}, "password": {password}, "_login_csrf": {m[1]}, "next": {"/"}}
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", h.srv.URL)
	if clientIP != "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func realUser(t *testing.T, h *harness, name string) {
	t.Helper()
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.db.CreateUser(context.Background(), &store.User{Username: name, PasswordHash: hash, Role: store.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
}

// Failed logins from one client must not lock the account for everyone.
func TestLoginLockoutPerClient(t *testing.T) {
	h := newHarness(t)
	realUser(t, h, "admin")
	for i := 0; i < 5; i++ {
		tryLogin(t, h, "admin", "wrong", "198.51.100.7")
	}
	if r := tryLogin(t, h, "admin", "correct horse battery", "198.51.100.7"); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("attacker not locked: %d", r.StatusCode)
	}
	if r := tryLogin(t, h, "admin", "correct horse battery", "192.0.2.10"); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("admin locked out from another address: %d", r.StatusCode)
	}
}

func TestLoginLockoutPerIP(t *testing.T) {
	h := newHarness(t)
	realUser(t, h, "admin")
	for i := 0; i < 20; i++ {
		tryLogin(t, h, "user"+strings.Repeat("x", i), "wrong", "198.51.100.8")
	}
	if r := tryLogin(t, h, "admin", "correct horse battery", "198.51.100.8"); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("spraying client not locked: %d", r.StatusCode)
	}
}

func TestLoginBodyAndUsernameBounded(t *testing.T) {
	h := newHarness(t)
	if r := tryLogin(t, h, strings.Repeat("a", 64<<10), "x", "198.51.100.9"); r.StatusCode == http.StatusSeeOther {
		t.Fatal("huge login form accepted")
	}
	if got := clipUsername(strings.Repeat("ü", 100)); len([]rune(got)) != maxUsername {
		t.Fatalf("clipUsername kept %d runes", len([]rune(got)))
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/dashboard":           "/dashboard",
		"/events/lan?x=1":      "/events/lan?x=1",
		"":                     "/",
		"https://evil.example": "/",
		"//evil.example":       "/",
		"/\\evil.example":      "/",
		"/\t/evil.example":     "/",
		"/\n/evil.example":     "/",
		"/a\\b":                "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
