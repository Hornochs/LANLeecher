package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWANValues(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest("GET", "/api/v1/events/"+f.pastSlug+"/wan?series=true", nil)
	req.Header.Set("Authorization", "Bearer "+f.full)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Links []struct {
			Name              string
			InternetDownBytes int64    `json:"internet_down_bytes"`
			DownBps           *float64 `json:"down_bps"`
			Ports             []struct {
				IfName string `json:"if_name"`
			}
			Series []struct {
				InternetDownBytes int64 `json:"internet_down_bytes"`
			}
		}
		Total struct {
			InternetDownBytes int64 `json:"internet_down_bytes"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Links) != 2 {
		t.Fatalf("links: %+v", body.Links)
	}
	// Sorted by sort, then name: "Backup LTE" (no ports) before "Fiber".
	lte, fiber := body.Links[0], body.Links[1]
	if lte.Name != "Backup LTE" || len(lte.Ports) != 0 || lte.InternetDownBytes != 0 {
		t.Fatalf("backup link: %+v", lte)
	}
	if fiber.Name != "Fiber" || len(fiber.Ports) != 1 || fiber.InternetDownBytes != 5e9 || len(fiber.Series) != 1 {
		t.Fatalf("fiber link: %+v", fiber)
	}
	if fiber.DownBps != nil {
		t.Fatal("ended event must not report a live rate")
	}
	if body.Total.InternetDownBytes != 5e9 {
		t.Fatalf("total %d", body.Total.InternetDownBytes)
	}
}
