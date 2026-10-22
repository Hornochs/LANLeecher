package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestSpecMatchesRoutes fails when a documented path has no route or a
// /api/v1 route is missing from the OpenAPI document.
func TestSpecMatchesRoutes(t *testing.T) {
	doc, err := Spec()
	if err != nil {
		t.Fatalf("openapi.yaml does not parse: %v", err)
	}
	paths, _ := doc["paths"].(map[string]any)
	documented := map[string]bool{}
	for p, item := range paths {
		for method := range item.(map[string]any) {
			documented[strings.ToUpper(method)+" "+p] = true
		}
	}

	a := &API{App: nil}
	routes := map[string]bool{}
	err = chi.Walk(a.routesForSpec(), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = "/api" + strings.TrimSuffix(route, "/")
		if strings.HasPrefix(route, "/api/v1/") {
			routes[method+" "+route] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for r := range routes {
		if !documented[r] {
			t.Errorf("route %s is not documented in openapi.yaml", r)
		}
	}
	for d := range documented {
		if strings.HasPrefix(d, "GET /health") {
			continue
		}
		if !routes[d] {
			t.Errorf("documented %s has no route", d)
		}
	}
}
