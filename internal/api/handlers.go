package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lan-dot-party/lanleecher/internal/auth"
	"github.com/lan-dot-party/lanleecher/internal/board"
	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/oui"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

// RankingEntry is one line of a ranking response.
type RankingEntry struct {
	Rank      int    `json:"rank"`
	Type      string `json:"type"`
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	BytesDown int64  `json:"bytes_down"`
	BytesUp   int64  `json:"bytes_up"`
	Hidden    *bool  `json:"hidden,omitempty"`
	// Only with scope read:clients:
	MAC      string   `json:"mac,omitempty"`
	MACs     []string `json:"macs,omitempty"`
	IP       string   `json:"ip,omitempty"`
	Hostname string   `json:"hostname,omitempty"`
	Vendor   string   `json:"vendor,omitempty"`
}

// EventJSON is an event in responses.
type EventJSON struct {
	Name      string     `json:"name"`
	Slug      string     `json:"slug"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	Active    bool       `json:"active"`
	Notes     string     `json:"notes"`
}

func eventJSON(e *store.Event) *EventJSON {
	if e == nil {
		return nil
	}
	return &EventJSON{Name: e.Name, Slug: e.Slug, StartedAt: e.StartedAt, EndedAt: e.EndedAt, Active: e.Active(), Notes: e.Notes}
}

func (a *API) rankings(w http.ResponseWriter, r *http.Request) {
	order := map[string]string{"download": "down", "upload": "up", "total": "total"}[chi.URLParam(r, "direction")]
	if order == "" {
		problem(w, http.StatusNotFound, "not_found", "Not Found", "use /rankings/download, /rankings/upload or /rankings/total")
		return
	}
	q := r.URL.Query()
	ev, ok := a.resolveEvent(r.Context(), w, q.Get("event"))
	if !ok {
		return
	}
	limit, ok := parseLimit(w, q.Get("limit"), 10)
	if !ok {
		return
	}
	from, ok := parseTime(w, "from", q.Get("from"))
	if !ok {
		return
	}
	to, ok := parseTime(w, "to", q.Get("to"))
	if !ok {
		return
	}
	var switchID int64
	if s := q.Get("switch"); s != "" {
		if switchID, _ = strconv.ParseInt(s, 10, 64); switchID <= 0 {
			problem(w, http.StatusBadRequest, "invalid_parameter", "Bad Request", "switch must be a switch ID")
			return
		}
	}
	includeHidden := parseBool(q.Get("include_hidden"))
	rq := store.RankingQuery{Order: order, Limit: limit, IncludeHidden: includeHidden, SwitchID: switchID, From: from, To: to}
	if ev != nil {
		rq.EventID = ev.ID
	}
	rows, err := a.DB.Rankings(r.Context(), rq)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	clients := principalOf(r.Context()).has(auth.ScopeReadClients)
	out := make([]RankingEntry, 0, len(rows))
	for _, row := range rows {
		e := RankingEntry{Rank: row.Rank, Type: row.Type, ID: row.Key, Name: row.Name, BytesDown: row.BytesDown, BytesUp: row.BytesUp}
		if includeHidden {
			h := row.Hidden
			e.Hidden = &h
		}
		if clients {
			e.MACs, e.IP, e.Hostname, e.Vendor = row.MACs, row.IP, row.Hostname, row.Vendor
			if row.Type == "mac" && len(row.MACs) > 0 {
				e.MAC = row.MACs[0]
			}
		} else {
			// Never leak a MAC through the display name without read:clients.
			e.Name = board.PublicName(row.Type, row.Key, row.Name)
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": eventJSON(ev), "order": chi.URLParam(r, "direction"), "entries": out})
}

func (a *API) listEvents(w http.ResponseWriter, r *http.Request) {
	events, err := a.DB.ListEvents(r.Context(), r.URL.Query().Get("q"), time.Time{}, time.Time{})
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]*EventJSON, 0, len(events))
	for _, e := range events {
		out = append(out, eventJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

func (a *API) getEvent(w http.ResponseWriter, r *http.Request) {
	ev, err := a.DB.EventBySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		problem(w, http.StatusNotFound, "event_not_found", "Not Found", "unknown event")
		return
	}
	sum, err := a.DB.EventSummary(r.Context(), ev.ID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": eventJSON(ev),
		"totals": map[string]any{"bytes_down": sum.BytesDown, "bytes_up": sum.BytesUp, "clients": sum.Clients}})
}

func (a *API) startEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Notes string `json:"notes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		problem(w, http.StatusBadRequest, "invalid_body", "Bad Request", `expected {"name": "...", "notes": "..."}`)
		return
	}
	ev, err := a.DB.StartEvent(r.Context(), body.Name, body.Notes)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	a.audit(r, "event.start", ev.Slug)
	writeJSON(w, http.StatusCreated, map[string]any{"event": eventJSON(ev)})
}

func (a *API) stopEvent(w http.ResponseWriter, r *http.Request) {
	active, err := a.DB.ActiveEvent(r.Context())
	if err != nil || active.Slug != chi.URLParam(r, "slug") {
		problem(w, http.StatusConflict, "event_not_active", "Conflict", "this event is not running")
		return
	}
	ev, err := a.DB.StopEvent(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	a.audit(r, "event.stop", ev.Slug)
	writeJSON(w, http.StatusOK, map[string]any{"event": eventJSON(ev)})
}

func (a *API) audit(r *http.Request, action, target string) {
	p := principalOf(r.Context())
	if p.user != nil {
		a.Audit(r, action, target, "api")
		return
	}
	a.DB.Audit(r.Context(), store.AuditEntry{Actor: "token:" + p.token.Name, Action: action, Target: target,
		Details: "api", IP: a.ClientIP(r)})
}

// HistoryJSON is an IP or hostname history entry.
type HistoryJSON struct {
	MAC       string    `json:"mac"`
	Value     string    `json:"value"`
	Source    string    `json:"source"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

func historyJSON(rows []store.HistoryRow) []HistoryJSON {
	out := make([]HistoryJSON, 0, len(rows))
	for _, h := range rows {
		out = append(out, HistoryJSON{MAC: h.MAC, Value: h.Value, Source: h.Source, FirstSeen: h.FirstSeen, LastSeen: h.LastSeen})
	}
	return out
}

func (a *API) clientByMAC(w http.ResponseWriter, r *http.Request) {
	mac := macaddr.Normalize(chi.URLParam(r, "mac"))
	if mac == "" {
		problem(w, http.StatusBadRequest, "invalid_mac", "Bad Request", "not a MAC address")
		return
	}
	devID, err := a.DB.DeviceOfMAC(r.Context(), mac)
	if err != nil {
		problem(w, http.StatusNotFound, "mac_not_found", "Not Found", "MAC never seen")
		return
	}
	limit := 0
	if s := r.URL.Query().Get("events"); s != "" {
		if limit, err = strconv.Atoi(s); err != nil || limit < 1 {
			problem(w, http.StatusBadRequest, "invalid_parameter", "Bad Request", "events must be a positive number")
			return
		}
	}
	dev, err := a.DB.GetDevice(r.Context(), devID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	labels, _ := a.DB.DeviceLabels(r.Context(), []int64{devID})
	events, err := a.DB.DeviceEvents(r.Context(), devID, limit)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	ips, _ := a.DB.DeviceIPHistory(r.Context(), devID)
	names, _ := a.DB.DeviceHostnameHistory(r.Context(), devID)
	locs, _ := a.DB.DeviceLocations(r.Context(), devID)

	type evTotal struct {
		Event     *EventJSON `json:"event"`
		BytesDown int64      `json:"bytes_down"`
		BytesUp   int64      `json:"bytes_up"`
	}
	type port struct {
		MAC       string    `json:"mac"`
		Port      string    `json:"port"`
		VLAN      int       `json:"vlan"`
		FirstSeen time.Time `json:"first_seen"`
		LastSeen  time.Time `json:"last_seen"`
	}
	evs := make([]evTotal, 0, len(events))
	for _, e := range events {
		evs = append(evs, evTotal{Event: eventJSON(e.Event), BytesDown: e.BytesDown, BytesUp: e.BytesUp})
	}
	ports := make([]port, 0, len(locs))
	for _, l := range locs {
		ports = append(ports, port{MAC: l.MAC, Port: l.PortName, VLAN: l.VLAN, FirstSeen: l.FirstSeen, LastSeen: l.LastSeen})
	}
	macs := make([]string, 0, len(dev.MACs))
	for _, m := range dev.MACs {
		macs = append(macs, m.MAC)
	}
	l := labels[devID]
	writeJSON(w, http.StatusOK, map[string]any{
		"mac": mac, "vendor": oui.LookupString(mac), "device_id": devID, "name": l.Name, "hostname": l.Hostname, "ip": l.IP, "macs": macs,
		"hidden": dev.Hidden, "events": evs, "ip_history": historyJSON(ips), "hostname_history": historyJSON(names),
		"ports": ports,
	})
}

func (a *API) clientTimeseries(w http.ResponseWriter, r *http.Request) {
	mac := macaddr.Normalize(chi.URLParam(r, "mac"))
	devID, err := a.DB.DeviceOfMAC(r.Context(), mac)
	if mac == "" || err != nil {
		problem(w, http.StatusNotFound, "mac_not_found", "Not Found", "MAC never seen")
		return
	}
	ev, ok := a.resolveEvent(r.Context(), w, r.URL.Query().Get("event"))
	if !ok {
		return
	}
	if ev == nil {
		problem(w, http.StatusBadRequest, "invalid_parameter", "Bad Request", "timeseries needs one event")
		return
	}
	buckets, err := a.DB.DeviceTimeseries(r.Context(), devID, ev.ID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	type point struct {
		Start     time.Time `json:"start"`
		BytesDown int64     `json:"bytes_down"`
		BytesUp   int64     `json:"bytes_up"`
	}
	pts := make([]point, 0, len(buckets))
	for _, b := range buckets {
		pts = append(pts, point{Start: b.Start, BytesDown: b.BytesDown, BytesUp: b.BytesUp})
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": eventJSON(ev), "bucket_seconds": int(store.BucketSize.Seconds()), "points": pts})
}

func (a *API) clientsByIP(w http.ResponseWriter, r *http.Request) {
	ip, err := netip.ParseAddr(chi.URLParam(r, "ip"))
	if err != nil {
		problem(w, http.StatusBadRequest, "invalid_ip", "Bad Request", "not an IP address")
		return
	}
	rows, err := a.DB.MACsByIP(r.Context(), ip.String())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ip": ip.String(), "entries": historyJSON(rows)})
}

func (a *API) clientsByHostname(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	rows, err := a.DB.MACsByHostname(r.Context(), name)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hostname": name, "entries": historyJSON(rows)})
}

func (a *API) searchClients(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		problem(w, http.StatusBadRequest, "invalid_parameter", "Bad Request", "q needs at least 2 characters")
		return
	}
	hits, err := a.DB.Search(r.Context(), q, 50)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	type hit struct {
		DeviceID int64     `json:"device_id"`
		Name     string    `json:"name"`
		MAC      string    `json:"mac"`
		IP       string    `json:"ip"`
		Hostname string    `json:"hostname"`
		Vendor   string    `json:"vendor"`
		LastSeen time.Time `json:"last_seen"`
		Match    string    `json:"match"`
	}
	out := make([]hit, 0, len(hits))
	for _, h := range hits {
		out = append(out, hit{h.DeviceID, h.Name, h.MAC, h.IP, h.Hostname, h.Vendor, h.LastSeen, h.Match})
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": q, "results": out})
}

func (a *API) ports(w http.ResponseWriter, r *http.Request) {
	ev, ok := a.resolveEvent(r.Context(), w, r.URL.Query().Get("event"))
	if !ok {
		return
	}
	if ev == nil {
		problem(w, http.StatusBadRequest, "invalid_parameter", "Bad Request", "ports needs one event")
		return
	}
	rows, err := a.DB.PortTrafficByEvent(r.Context(), ev.ID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	type port struct {
		ID             int64  `json:"id"`
		Switch         string `json:"switch"`
		IfName         string `json:"if_name"`
		Label          string `json:"label"`
		Role           string `json:"role"`
		Hidden         bool   `json:"hidden"`
		BytesDown      int64  `json:"bytes_down"`
		BytesUp        int64  `json:"bytes_up"`
		UnassignedDown int64  `json:"unassigned_bytes_down"`
		UnassignedUp   int64  `json:"unassigned_bytes_up"`
	}
	out := make([]port, 0, len(rows))
	for _, p := range rows {
		out = append(out, port{p.Port.ID, p.Port.SwitchName, p.Port.ShortName(), p.Port.Label, string(p.Port.Role),
			p.Port.Hidden, p.BytesDown, p.BytesUp, p.UnassignedDown, p.UnassignedUp})
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": eventJSON(ev), "ports": out})
}

func (a *API) boardParams(w http.ResponseWriter, r *http.Request) (*store.Event, int, bool) {
	limit := 10
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || !board.ValidLimit(n) {
			problem(w, http.StatusBadRequest, "invalid_parameter", "Bad Request", "limit must be 10, 20 or 50")
			return nil, 0, false
		}
		limit = n
	}
	var ev *store.Event
	if slug := r.URL.Query().Get("event"); slug != "" {
		e, err := a.DB.EventBySlug(r.Context(), slug)
		if err != nil {
			problem(w, http.StatusNotFound, "event_not_found", "Not Found", "unknown event")
			return nil, 0, false
		}
		ev = e
	}
	return ev, limit, true
}

func (a *API) board(w http.ResponseWriter, r *http.Request) {
	ev, limit, ok := a.boardParams(w, r)
	if !ok {
		return
	}
	b, err := a.Board.Build(r.Context(), ev, limit)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// boardStream sends the board as Server-Sent Events after every poll.
func (a *API) boardStream(w http.ResponseWriter, r *http.Request) {
	ev, limit, ok := a.boardParams(w, r)
	if !ok {
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	updates, cancel := a.Board.Subscribe()
	defer cancel()
	send := func() bool {
		b, err := a.Board.Build(r.Context(), ev, limit)
		if err != nil {
			a.Log.Warn("board stream", "err", err)
			return true
		}
		data, _ := json.Marshal(b)
		if _, err := fmt.Fprintf(w, "event: board\ndata: %s\n\n", data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send() {
		return
	}
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-updates:
			if !send() {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

func (a *API) metrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	var b strings.Builder
	down, up := a.Sched.Throughput()
	fmt.Fprintf(&b, "# HELP lanleecher_throughput_bits_per_second Current throughput of all non-uplink ports.\n")
	fmt.Fprintf(&b, "# TYPE lanleecher_throughput_bits_per_second gauge\n")
	fmt.Fprintf(&b, "lanleecher_throughput_bits_per_second{direction=\"down\"} %.0f\n", down)
	fmt.Fprintf(&b, "lanleecher_throughput_bits_per_second{direction=\"up\"} %.0f\n", up)

	if ev, err := a.DB.ActiveEvent(ctx); err == nil {
		if sum, err := a.DB.EventSummary(ctx, ev.ID); err == nil {
			fmt.Fprintf(&b, "# HELP lanleecher_event_bytes_total Traffic of the active event.\n")
			fmt.Fprintf(&b, "# TYPE lanleecher_event_bytes_total counter\n")
			fmt.Fprintf(&b, "lanleecher_event_bytes_total{event=%q,direction=\"down\"} %d\n", ev.Slug, sum.BytesDown)
			fmt.Fprintf(&b, "lanleecher_event_bytes_total{event=%q,direction=\"up\"} %d\n", ev.Slug, sum.BytesUp)
			fmt.Fprintf(&b, "# HELP lanleecher_event_clients Distinct MACs with traffic in the active event.\n")
			fmt.Fprintf(&b, "# TYPE lanleecher_event_clients gauge\n")
			fmt.Fprintf(&b, "lanleecher_event_clients{event=%q} %d\n", ev.Slug, sum.Clients)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		a.internal(w, r, err)
		return
	}
	switches, err := a.DB.ListSwitches(ctx)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	sort.Slice(switches, func(i, j int) bool { return switches[i].Name < switches[j].Name })
	fmt.Fprintf(&b, "# HELP lanleecher_switch_up Whether the last poll of a switch succeeded.\n")
	fmt.Fprintf(&b, "# TYPE lanleecher_switch_up gauge\n")
	for _, s := range switches {
		v := 0
		if s.LastPollOK && s.Enabled {
			v = 1
		}
		fmt.Fprintf(&b, "lanleecher_switch_up{switch=%q} %d\n", s.Name, v)
	}
	w.Write([]byte(b.String()))
}
