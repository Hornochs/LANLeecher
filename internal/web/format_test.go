package web

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/store"
)

func TestLineChartTooltipData(t *testing.T) {
	start := time.Unix(1_700_000_000, 0).Truncate(store.BucketSize)
	buckets := []store.Bucket{
		{Start: start, BytesDown: 375_000_000, BytesUp: 37_500_000},
		// One bucket without traffic in between must show up as zero.
		{Start: start.Add(2 * store.BucketSize), BytesDown: 75_000_000, BytesUp: 0},
	}
	got := string(lineChart(buckets))
	for _, want := range []string{
		`<div class="chart-wrap"><svg class="chart"`,
		`data-start="` + strconv.FormatInt(start.Unix(), 10) + `"`,
		`data-step="300"`,
		`data-down="10000000,0,2000000"`,
		`data-up="1000000,0,0"`,
		`</svg></div>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("chart is missing %q:\n%s", want, got)
		}
	}
}


func TestLineChartDetailData(t *testing.T) {
	buckets := []store.Bucket{{Start: time.Unix(1_700_000_100, 0), BytesDown: 1}}
	if got := string(lineChart(buckets)); strings.Contains(got, "data-detail") {
		t.Errorf("plain chart has a click detail:\n%s", got)
	}
	got := string(lineChartDetail(buckets, "/partials/traffic-window?event=a&b", "chart-detail"))
	for _, want := range []string{
		`data-detail="/partials/traffic-window?event=a&amp;b"`,
		`data-detail-target="chart-detail">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("chart is missing %q:\n%s", want, got)
		}
	}
}
