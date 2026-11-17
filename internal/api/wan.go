package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

// WANPortRefJSON names a WAN port.
type WANPortRefJSON struct {
	ID     int64  `json:"id"`
	Switch string `json:"switch"`
	IfName string `json:"if_name"`
	Label  string `json:"label"`
}

// WANLinkJSON is a WAN link without traffic.
type WANLinkJSON struct {
	ID       int64            `json:"id"`
	Name     string           `json:"name"`
	Sort     int              `json:"sort"`
	DownMbps int64            `json:"down_mbps"`
	UpMbps   int64            `json:"up_mbps"`
	Notes    string           `json:"notes"`
	Ports    []WANPortRefJSON `json:"ports"`
}

func portRefJSON(p *store.Port) WANPortRefJSON {
	return WANPortRefJSON{ID: p.ID, Switch: p.SwitchName, IfName: p.IfName, Label: p.Label}
}

func (a *API) listWANLinks(w http.ResponseWriter, r *http.Request) {
	links, err := a.DB.ListWANLinks(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]WANLinkJSON, 0, len(links))
	for _, l := range links {
		j := WANLinkJSON{ID: l.ID, Name: l.Name, Sort: l.Sort, DownMbps: l.DownMbps, UpMbps: l.UpMbps, Notes: l.Notes,
			Ports: []WANPortRefJSON{}}
		for _, p := range l.Ports {
			j.Ports = append(j.Ports, portRefJSON(p))
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

// WANTrafficJSON is traffic in the internet's point of view: down is what
// the LAN downloaded. Rates are bit/s, null when the event is not running or
// no recent reading exists.
type WANTrafficJSON struct {
	InternetDownBytes int64    `json:"internet_down_bytes"`
	InternetUpBytes   int64    `json:"internet_up_bytes"`
	DownBps           *float64 `json:"down_bps"`
	UpBps             *float64 `json:"up_bps"`
}

// WANPortTrafficJSON is one WAN port with its traffic.
type WANPortTrafficJSON struct {
	WANPortRefJSON
	WANTrafficJSON
}

// WANBucketJSON is one 5-minute bucket of a WAN link.
type WANBucketJSON struct {
	Start             time.Time `json:"start"`
	InternetDownBytes int64     `json:"internet_down_bytes"`
	InternetUpBytes   int64     `json:"internet_up_bytes"`
}

// WANLinkTrafficJSON is one WAN link with its traffic.
type WANLinkTrafficJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	DownMbps int64  `json:"down_mbps"`
	UpMbps   int64  `json:"up_mbps"`
	WANTrafficJSON
	// Utilisation of the booked bandwidth (0–1), null when unknown.
	UtilDown *float64             `json:"util_down"`
	UtilUp   *float64             `json:"util_up"`
	Ports    []WANPortTrafficJSON `json:"ports"`
	// Series is present only with ?series=true.
	Series *[]WANBucketJSON `json:"series,omitempty"`
}

// wanActive serves /v1/wan: the active event, or ?event=<slug>.
func (a *API) wanActive(w http.ResponseWriter, r *http.Request) {
	param := r.URL.Query().Get("event")
	if param == "all" {
		problem(w, http.StatusBadRequest, "invalid_parameter", "Bad Request", "event=all is not supported here")
		return
	}
	ev, ok := a.resolveEvent(r.Context(), w, param)
	if !ok {
		return
	}
	a.wanTraffic(w, r, ev)
}

// wanEvent serves /v1/events/{slug}/wan.
func (a *API) wanEvent(w http.ResponseWriter, r *http.Request) {
	ev, err := a.DB.EventBySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		problem(w, http.StatusNotFound, "event_not_found", "Not Found", "unknown event")
		return
	}
	a.wanTraffic(w, r, ev)
}

func (a *API) wanTraffic(w http.ResponseWriter, r *http.Request, ev *store.Event) {
	q := r.URL.Query()
	series := false
	switch q.Get("series") {
	case "", "0", "false":
	case "1", "true":
		series = true
	default:
		problem(w, http.StatusBadRequest, "invalid_parameter", "Bad Request", "series must be true or false")
		return
	}
	links, err := a.DB.WANTraffic(r.Context(), ev.ID, series)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if sel := strings.TrimSpace(q.Get("link")); sel != "" {
		var keep []store.WANLinkTraffic
		id, numErr := strconv.ParseInt(sel, 10, 64)
		for _, l := range links {
			if (numErr == nil && l.Link.ID == id) || strings.EqualFold(l.Link.Name, sel) {
				keep = append(keep, l)
			}
		}
		if len(keep) == 0 {
			problem(w, http.StatusNotFound, "wan_link_not_found", "Not Found", "unknown WAN link "+sel)
			return
		}
		links = keep
	}

	var rates map[int64]rateView
	if ev.Active() && a.Sched != nil {
		rates = map[int64]rateView{}
		for id, rt := range a.Sched.LivePortRates() {
			down, up := accounting.WANDirection(rt.DownBps, rt.UpBps)
			rates[id] = rateView{down: down, up: up}
		}
	}

	total := WANTrafficJSON{}
	var totalDown, totalUp float64
	haveRate := false
	out := make([]WANLinkTrafficJSON, 0, len(links))
	for _, l := range links {
		j := WANLinkTrafficJSON{ID: l.Link.ID, Name: l.Link.Name, DownMbps: l.Link.DownMbps, UpMbps: l.Link.UpMbps,
			WANTrafficJSON: WANTrafficJSON{InternetDownBytes: l.InternetDown, InternetUpBytes: l.InternetUp},
			Ports:          []WANPortTrafficJSON{}}
		var linkDown, linkUp float64
		linkRate := false
		for _, p := range l.Ports {
			pj := WANPortTrafficJSON{WANPortRefJSON: portRefJSON(p.Port),
				WANTrafficJSON: WANTrafficJSON{InternetDownBytes: p.InternetDown, InternetUpBytes: p.InternetUp}}
			if rt, ok := rates[p.Port.ID]; ok {
				pj.DownBps, pj.UpBps = ptr(rt.down), ptr(rt.up)
				linkDown += rt.down
				linkUp += rt.up
				linkRate = true
			}
			j.Ports = append(j.Ports, pj)
		}
		if linkRate {
			j.DownBps, j.UpBps = ptr(linkDown), ptr(linkUp)
			if l.Link.DownMbps > 0 {
				j.UtilDown = ptr(linkDown / (float64(l.Link.DownMbps) * 1e6))
			}
			if l.Link.UpMbps > 0 {
				j.UtilUp = ptr(linkUp / (float64(l.Link.UpMbps) * 1e6))
			}
			totalDown += linkDown
			totalUp += linkUp
			haveRate = true
		}
		if series {
			buckets := make([]WANBucketJSON, 0, len(l.Series))
			for _, b := range l.Series {
				buckets = append(buckets, WANBucketJSON{Start: b.Start, InternetDownBytes: b.BytesDown, InternetUpBytes: b.BytesUp})
			}
			j.Series = &buckets
		}
		total.InternetDownBytes += l.InternetDown
		total.InternetUpBytes += l.InternetUp
		out = append(out, j)
	}
	if haveRate {
		total.DownBps, total.UpBps = ptr(totalDown), ptr(totalUp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": eventJSON(ev), "links": out, "total": total})
}

type rateView struct{ down, up float64 }

func ptr(v float64) *float64 { return &v }
