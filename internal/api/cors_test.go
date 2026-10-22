package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBoardCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := boardCORS(next)

	pre := httptest.NewRecorder()
	h.ServeHTTP(pre, httptest.NewRequest(http.MethodOptions, "/v1/board", nil))
	if pre.Code != http.StatusNoContent || pre.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight: %d %v", pre.Code, pre.Header())
	}
	if pre.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials must not be allowed cross-origin")
	}

	other := httptest.NewRecorder()
	h.ServeHTTP(other, httptest.NewRequest(http.MethodGet, "/v1/clients/search", nil))
	if other.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS must only apply to the board")
	}
}
