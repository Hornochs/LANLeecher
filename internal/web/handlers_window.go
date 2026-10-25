package web

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/store"
)

// windowLimit is the number of entries in the chart detail.
const windowLimit = 10

type windowData struct {
	Event      *store.Event
	From, To   time.Time
	Total      store.PortTraffic
	Unassigned store.PortTraffic
	// DownBps and UpBps are the average rates over the window.
	DownBps, UpBps float64
	View           RankingView
	RankingsURL    string
}

// trafficWindow shows who caused the traffic of a time range of the event
// timeline (htmx partial, loaded by a click into the chart). Hidden ports
// and devices are included and marked, because the timeline counts them too.
func (s *Server) trafficWindow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	from, err1 := strconv.ParseInt(q.Get("from"), 10, 64)
	to, err2 := strconv.ParseInt(q.Get("to"), 10, 64)
	if err1 != nil || err2 != nil || from <= 0 || to <= from {
		s.errorPage(w, r, http.StatusBadRequest, "error.bad_request")
		return
	}
	ev, err := s.DB.EventBySlug(ctx, q.Get("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := &windowData{Event: ev, From: time.Unix(from, 0).UTC(), To: time.Unix(to, 0).UTC()}
	if d.Total, d.Unassigned, err = s.DB.WindowTotals(ctx, ev.ID, d.From, d.To); err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.DB.Rankings(ctx, store.RankingQuery{EventID: ev.ID, From: d.From, To: d.To, Order: "total",
		Limit: windowLimit, IncludeHidden: true})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The bucket that is still running has only been filled up to now.
	end := d.To
	if now := store.Now(); now.Before(end) {
		end = now
	}
	if secs := end.Sub(d.From).Seconds(); secs > 0 {
		d.DownBps = float64(d.Total.BytesDown) * 8 / secs
		d.UpBps = float64(d.Total.BytesUp) * 8 / secs
	}
	v := url.Values{"event": {ev.Slug}, "dir": {"total"}, "include_hidden": {"1"},
		"from": {formatTimeInput(d.From)}, "to": {formatTimeInput(d.To)}}
	d.RankingsURL = "/rankings?" + v.Encode()
	p := s.page(r, "", "dashboard", nil)
	d.View = newRankingView(p, rows, true)
	p.Data = d
	s.partial(w, r, "dashboard", "traffic_window", p)
}
