package web

// Network maps: switches and internet connections placed on a background
// image, the links between them (LLDP, learned MAC addresses, drawn by hand)
// with their current rates. Everyone logged in can view maps; only admins
// change them. The code comes from ERS-Proxy.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // background images: GIF
	_ "image/jpeg" // background images: JPEG
	_ "image/png"  // background images: PNG
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
	"github.com/lan-dot-party/lanleecher/internal/app"
	"github.com/lan-dot-party/lanleecher/internal/collector"
	"github.com/lan-dot-party/lanleecher/internal/scheduler"
	"github.com/lan-dot-party/lanleecher/internal/store"
	"github.com/lan-dot-party/lanleecher/internal/topology"
)

// maxMapBackground is the largest background image accepted.
const maxMapBackground = 8 << 20

// maxMapUploadBody is the largest request body of a background upload: the
// image plus multipart overhead.
const maxMapUploadBody = 9 << 20

// maxMapPixels bounds width × height of a background image.
const maxMapPixels = 50_000_000

// Default size of a map without background image (its coordinate space).
const defaultMapWidth, defaultMapHeight = 1600, 900

type mapsData struct {
	Maps    []*store.Map
	Current *store.Map
	Admin   bool
}

func (s *Server) loadMap(w http.ResponseWriter, r *http.Request) (*store.Map, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "error.not_found")
		return nil, false
	}
	m, err := s.DB.MapByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.errorPage(w, r, http.StatusNotFound, "error.not_found")
		return nil, false
	}
	if err != nil {
		s.fail(w, r, err)
		return nil, false
	}
	return m, true
}

// mapsPage shows the first map (or none).
func (s *Server) mapsPage(w http.ResponseWriter, r *http.Request) {
	maps, err := s.DB.ListMaps(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(maps) > 0 {
		redirect(w, r, fmt.Sprintf("/maps/%d", maps[0].ID))
		return
	}
	s.renderMap(w, r, maps, nil)
}

func (s *Server) mapPage(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMap(w, r)
	if !ok {
		return
	}
	maps, err := s.DB.ListMaps(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderMap(w, r, maps, m)
}

func (s *Server) renderMap(w http.ResponseWriter, r *http.Request, maps []*store.Map, m *store.Map) {
	p := s.page(r, translate(language(r), "maps.title"), "maps", mapsData{Maps: maps, Current: m, Admin: app.User(r.Context()).Can(store.RoleAdmin)})
	if m != nil {
		p.Title = m.Name + " · " + p.T("maps.title")
	}
	p.Wide = true
	s.render(w, r, "maps", p)
}

// mapBackground serves a map's background image.
func (s *Server) mapBackground(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMap(w, r)
	if !ok {
		return
	}
	data, mediaType, hash, err := s.DB.MapBackground(r.Context(), m.ID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", mediaType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=3600")
	h.Set("ETag", `"`+hash+`"`)
	http.ServeContent(w, r, "", m.Updated, bytes.NewReader(data))
}

// --- state for the browser ---

// Link sources in addition to the topology package's "lldp" and "fdb".
const (
	sourceManual = "manual"
	sourceWAN    = "wan"
)

type mapEndJSON struct {
	Node    string `json:"node"`   // key of the node on the map: "s<switch ID>" or "w<WAN link ID>"
	Device  int64  `json:"device"` // switch ID, 0 for a WAN node
	IfIndex int    `json:"ifindex"`
	Port    string `json:"port"`
	Speed   uint64 `json:"speed"` // bit/s
	Up      bool   `json:"up"`
	// OutBps is what the switch sends out of the port (client download),
	// InBps what it receives (client upload).
	OutBps float64 `json:"-"`
	InBps  float64 `json:"-"`
	Known  bool    `json:"-"` // live rates known
}

type mapLinkJSON struct {
	Source string     `json:"source"` // lldp, fdb, manual, wan
	ID     int64      `json:"id,omitempty"`
	A      mapEndJSON `json:"a"`
	B      mapEndJSON `json:"b"`
	AB     float64    `json:"ab_bps"` // A → B
	BA     float64    `json:"ba_bps"` // B → A
	Util   float64    `json:"util"`   // percent of the link speed, higher direction
	Rates  bool       `json:"rates"`  // rates known
}

type mapPortJSON struct {
	IfIndex int    `json:"ifindex"`
	Name    string `json:"name"`
}

type mapNodeJSON struct {
	Key    string        `json:"key"`
	Kind   string        `json:"kind"` // switch, wan
	ID     int64         `json:"id"`
	Name   string        `json:"name"`
	X      float64       `json:"x"`
	Y      float64       `json:"y"`
	Status string        `json:"status"` // up, down, unknown, archived (switches only)
	Error  string        `json:"error,omitempty"`
	Href   string        `json:"href,omitempty"`  // page to open on click
	Ports  []mapPortJSON `json:"ports,omitempty"` // admins only, for drawing links
}

type mapAvailJSON struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type mapStateJSON struct {
	Map struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Refresh int    `json:"refresh"`
		Width   int    `json:"width"`
		Height  int    `json:"height"`
		BG      string `json:"bg,omitempty"`
	} `json:"map"`
	Nodes     []mapNodeJSON  `json:"nodes"`
	Links     []mapLinkJSON  `json:"links"`
	Hidden    []mapLinkJSON  `json:"hidden,omitempty"`    // admins only
	Available []mapAvailJSON `json:"available,omitempty"` // admins only: switches and WAN links to add
	Time      time.Time      `json:"time"`
}

func switchKey(id int64) string { return "s" + strconv.FormatInt(id, 10) }
func wanKey(id int64) string    { return "w" + strconv.FormatInt(id, 10) }

// topologyInput builds the topology package's input from the stored data:
// every switch is a device, archived ones without a forwarding table.
func (s *Server) topologyInput(ctx context.Context, switches []*store.Switch, ports []*store.Port) ([]topology.Device, error) {
	neighbors, err := s.DB.ListLLDPNeighbors(ctx)
	if err != nil {
		return nil, err
	}
	portsOf := map[int64][]*store.Port{}
	byIf := map[int64]map[int]*store.Port{}
	for _, p := range ports {
		portsOf[p.SwitchID] = append(portsOf[p.SwitchID], p)
		if byIf[p.SwitchID] == nil {
			byIf[p.SwitchID] = map[int]*store.Port{}
		}
		byIf[p.SwitchID][p.IfIndex] = p
	}
	devs := make([]topology.Device, 0, len(switches))
	for _, sw := range switches {
		snap := &topology.Snapshot{System: topology.System{Name: sw.SysName, ChassisID: sw.ChassisID, BaseMAC: sw.BaseMAC}}
		nbByIf := map[int]*topology.Neighbor{}
		for _, n := range neighbors[sw.ID] {
			nbByIf[n.IfIndex] = &topology.Neighbor{SysName: n.SysName, PortID: n.PortID, PortDesc: n.PortDesc, ChassisID: n.ChassisID}
		}
		for _, p := range portsOf[sw.ID] {
			tp := &topology.Port{IfIndex: p.IfIndex, Name: p.IfName, Descr: p.IfDescr, Alias: p.IfAlias,
				LAG: p.LAGIfIndex, Neighbor: nbByIf[p.IfIndex]}
			if lag := byIf[sw.ID][p.LAGIfIndex]; lag != nil {
				tp.LAGName = lag.ShortName()
			}
			snap.Ports = append(snap.Ports, tp)
		}
		if !sw.Archived && s.Sched != nil {
			snap.OwnMACs = s.Sched.OwnMACs(sw.ID)
			for ifIndex, macs := range s.Sched.FDB(sw.ID) {
				for _, m := range macs {
					snap.FDB = append(snap.FDB, topology.FDBEntry{MAC: m.String(), IfIndex: ifIndex})
				}
			}
		}
		devs = append(devs, topology.Device{ID: sw.ID, Name: sw.Name, Snap: snap})
	}
	return devs, nil
}

// switchStatus is the status shown for a switch node.
func switchStatus(sw *store.Switch) (status, errText string) {
	switch {
	case sw.Archived:
		return "archived", ""
	case sw.LastPollAt == nil || !sw.Enabled:
		return "unknown", ""
	case sw.LastPollOK:
		return "up", ""
	}
	return "down", sw.LastError
}

// mapState returns nodes, links and rates of a map as JSON.
func (s *Server) mapState(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMap(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	admin := app.User(ctx).Can(store.RoleAdmin)
	nodes, err := s.DB.MapNodes(ctx, m.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	wanNodes, err := s.DB.MapWANNodes(ctx, m.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stored, err := s.DB.MapLinks(ctx, m.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	switches, err := s.DB.ListSwitches(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ports, err := s.DB.ListAllPorts(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	wans, err := s.DB.ListWANLinks(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	topo, err := s.topologyInput(ctx, switches, ports)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var live map[int64]scheduler.Rate
	if s.Sched != nil {
		live = s.Sched.LivePortRates()
	}
	swByID := map[int64]*store.Switch{}
	for _, sw := range switches {
		swByID[sw.ID] = sw
	}
	wanByID := map[int64]*store.WANLink{}
	for _, l := range wans {
		wanByID[l.ID] = l
	}
	portBy := map[int64]map[int]*store.Port{}
	for _, p := range ports {
		if portBy[p.SwitchID] == nil {
			portBy[p.SwitchID] = map[int]*store.Port{}
		}
		portBy[p.SwitchID][p.IfIndex] = p
	}

	st := mapStateJSON{Nodes: []mapNodeJSON{}, Links: []mapLinkJSON{}}
	st.Map.ID, st.Map.Name, st.Map.Refresh = m.ID, m.Name, m.Refresh
	st.Map.Width, st.Map.Height = defaultMapWidth, defaultMapHeight
	if m.HasBackground() {
		st.Map.Width, st.Map.Height = m.BGWidth, m.BGHeight
		st.Map.BG = fmt.Sprintf("/maps/%d/background?v=%s", m.ID, m.BGHash)
	}
	st.Time = time.Now().UTC()

	onMap := map[int64]bool{}
	for _, n := range nodes {
		sw := swByID[n.SwitchID]
		if sw == nil {
			continue
		}
		onMap[sw.ID] = true
		nj := mapNodeJSON{Key: switchKey(sw.ID), Kind: "switch", ID: sw.ID, Name: sw.Name, X: n.X, Y: n.Y,
			Href: fmt.Sprintf("/switches/%d", sw.ID)}
		nj.Status, nj.Error = switchStatus(sw)
		if admin {
			nj.Ports = mapPorts(portBy[sw.ID])
		}
		st.Nodes = append(st.Nodes, nj)
	}
	wanOnMap := map[int64]bool{}
	for _, n := range wanNodes {
		l := wanByID[n.WANLinkID]
		if l == nil {
			continue
		}
		wanOnMap[l.ID] = true
		nj := mapNodeJSON{Key: wanKey(l.ID), Kind: "wan", ID: l.ID, Name: l.Name, X: n.X, Y: n.Y, Status: "wan"}
		if admin {
			nj.Href = "/admin/wan"
		}
		st.Nodes = append(st.Nodes, nj)
	}

	end := func(sw int64, ifIndex int, port string) mapEndJSON {
		e := mapEndJSON{Node: switchKey(sw), Device: sw, IfIndex: ifIndex, Port: port}
		if p := portBy[sw][ifIndex]; p != nil && ifIndex != 0 {
			e.Speed, e.Up = p.SpeedMbps*1_000_000, p.OperUp
			if rt, ok := live[p.ID]; ok {
				e.Known, e.OutBps, e.InBps = true, rt.DownBps, rt.UpBps
			}
			if e.Port == "" {
				e.Port = p.ShortName()
			}
		}
		return e
	}
	hidden := func(a, b mapEndJSON) bool {
		for i := range stored {
			if stored[i].Kind == store.MapLinkHidden && stored[i].Same(a.Device, a.IfIndex, b.Device, b.IfIndex) {
				return true
			}
		}
		return false
	}
	for _, l := range topology.Build(topo) {
		if !onMap[l.A.DeviceID] || !onMap[l.B.DeviceID] {
			continue
		}
		lj := mapLinkJSON{Source: l.Source, A: end(l.A.DeviceID, l.A.IfIndex, l.A.Port), B: end(l.B.DeviceID, l.B.IfIndex, l.B.Port)}
		if hidden(lj.A, lj.B) {
			continue
		}
		st.Links = append(st.Links, rates(lj))
	}
	for _, sl := range stored {
		if !onMap[sl.ASwitch] || !onMap[sl.BSwitch] {
			continue
		}
		lj := mapLinkJSON{Source: sl.Kind, ID: sl.ID, A: end(sl.ASwitch, sl.AIf, ""), B: end(sl.BSwitch, sl.BIf, "")}
		switch sl.Kind {
		case store.MapLinkManual:
			lj.Source = sourceManual
			st.Links = append(st.Links, rates(lj))
		case store.MapLinkHidden:
			if admin {
				st.Hidden = append(st.Hidden, lj)
			}
		}
	}
	// Internet connections: one link to each WAN port whose switch is on the map.
	for _, l := range wans {
		if !wanOnMap[l.ID] {
			continue
		}
		st.Links = append(st.Links, wanLinks(l, end, onMap, live)...)
	}
	if admin {
		for _, sw := range switches {
			if !onMap[sw.ID] {
				st.Available = append(st.Available, mapAvailJSON{Kind: "switch", ID: sw.ID, Name: sw.Name})
			}
		}
		for _, l := range wans {
			if !wanOnMap[l.ID] {
				st.Available = append(st.Available, mapAvailJSON{Kind: "wan", ID: l.ID, Name: l.Name})
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(st)
}

// wanLinks draws a WAN link to each of its ports on switches that are on the
// map. A is the internet connection, B the switch port; A → B is the
// internet download. Each line shows the rates of its port; the colour shows
// the utilisation of the whole connection: against the booked bandwidth when
// it is set, else against the port speed.
func wanLinks(l *store.WANLink, end func(int64, int, string) mapEndJSON, onMap map[int64]bool,
	live map[int64]scheduler.Rate) []mapLinkJSON {
	var out []mapLinkJSON
	var totalDown, totalUp float64
	for _, p := range l.Ports {
		if r, ok := live[p.ID]; ok {
			down, up := accounting.WANDirection(r.DownBps, r.UpBps)
			totalDown += down
			totalUp += up
		}
	}
	for _, p := range l.Ports {
		if !onMap[p.SwitchID] {
			continue
		}
		b := end(p.SwitchID, p.IfIndex, "")
		lj := mapLinkJSON{Source: sourceWAN, A: mapEndJSON{Node: wanKey(l.ID), Port: l.Name, Up: true}, B: b}
		if r, ok := live[p.ID]; ok {
			lj.AB, lj.BA = accounting.WANDirection(r.DownBps, r.UpBps)
			lj.Rates = true
			lj.A.Speed = b.Speed
			switch {
			case l.DownMbps > 0 || l.UpMbps > 0:
				if l.DownMbps > 0 {
					lj.Util = 100 * totalDown / (float64(l.DownMbps) * 1e6)
				}
				if l.UpMbps > 0 {
					lj.Util = max(lj.Util, 100*totalUp/(float64(l.UpMbps)*1e6))
				}
			case b.Speed > 0:
				lj.Util = 100 * max(lj.AB, lj.BA) / float64(b.Speed)
			}
		}
		out = append(out, lj)
	}
	return out
}

// rates fills the rates of a link between switches from the port at A, else
// from the port at B (reversed). A's out rate is the traffic towards B.
func rates(l mapLinkJSON) mapLinkJSON {
	speed := l.A.Speed
	switch {
	case l.A.Known:
		l.AB, l.BA, l.Rates = l.A.OutBps, l.A.InBps, true
	case l.B.Known:
		l.AB, l.BA, l.Rates, speed = l.B.InBps, l.B.OutBps, true, l.B.Speed
	}
	if speed == 0 {
		speed = max(l.A.Speed, l.B.Speed)
	}
	if l.Rates && speed > 0 {
		l.Util = 100 * max(l.AB, l.BA) / float64(speed)
	}
	return l
}

// mapPorts lists the physical ports and LAGs of a switch for the link editor.
func mapPorts(ports map[int]*store.Port) []mapPortJSON {
	var out []mapPortJSON
	for _, p := range ports {
		lag := (&collector.Interface{Type: p.IfType, Name: p.IfName, Descr: p.IfDescr}).IsLAG()
		if p.Physical || lag {
			out = append(out, mapPortJSON{IfIndex: p.IfIndex, Name: p.ShortName()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IfIndex < out[j].IfIndex })
	return out
}

// --- admin actions ---

func (s *Server) mapCreate(w http.ResponseWriter, r *http.Request) {
	m := &store.Map{Name: r.PostFormValue("name")}
	if err := s.DB.CreateMap(r.Context(), m); err != nil {
		s.mapFormError(w, r, err)
		return
	}
	s.Audit(r, "map.create", m.Name, "")
	redirect(w, r, fmt.Sprintf("/maps/%d?edit=1", m.ID))
}

func (s *Server) mapUpdate(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMap(w, r)
	if !ok {
		return
	}
	m.Name = r.PostFormValue("name")
	m.Refresh, _ = strconv.Atoi(strings.TrimSpace(r.PostFormValue("refresh")))
	m.Sort, _ = strconv.Atoi(strings.TrimSpace(r.PostFormValue("sort")))
	if err := s.DB.UpdateMap(r.Context(), m); err != nil {
		s.mapFormError(w, r, err)
		return
	}
	s.Audit(r, "map.update", m.Name, fmt.Sprintf("refresh=%ds", m.Refresh))
	redirect(w, r, fmt.Sprintf("/maps/%d?edit=1", m.ID))
}

func (s *Server) mapDelete(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMap(w, r)
	if !ok {
		return
	}
	if err := s.DB.DeleteMap(r.Context(), m.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "map.delete", m.Name, "")
	redirect(w, r, "/maps")
}

func (s *Server) mapFormError(w http.ResponseWriter, r *http.Request, err error) {
	key := "maps.err.invalid"
	if errors.Is(err, store.ErrExists) {
		key = "maps.err.exists"
	}
	s.errorPage(w, r, http.StatusUnprocessableEntity, key)
}

// mapBackgroundUpload stores or removes the background image. Only PNG,
// JPEG and GIF are accepted (decoded header, not the file name); SVG is
// refused because it can carry script.
func (s *Server) mapBackgroundUpload(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMap(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if r.PostFormValue("remove") == "1" {
		if err := s.DB.SetMapBackground(ctx, m.ID, nil, "", 0, 0); err != nil {
			s.fail(w, r, err)
			return
		}
		s.Audit(r, "map.background", m.Name, "removed")
		redirect(w, r, fmt.Sprintf("/maps/%d?edit=1", m.ID))
		return
	}
	if r.MultipartForm == nil { // the CSRF check has parsed it already
		r.Body = http.MaxBytesReader(w, r.Body, maxMapUploadBody)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			s.errorPage(w, r, http.StatusRequestEntityTooLarge, "maps.err.image_size")
			return
		}
	}
	f, _, err := r.FormFile("image")
	if err != nil {
		s.errorPage(w, r, http.StatusUnprocessableEntity, "maps.err.image")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMapBackground+1))
	if err != nil || len(data) > maxMapBackground {
		s.errorPage(w, r, http.StatusRequestEntityTooLarge, "maps.err.image_size")
		return
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	types := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}
	mediaType := types[format]
	if err != nil || mediaType == "" || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxMapPixels {
		s.errorPage(w, r, http.StatusUnprocessableEntity, "maps.err.image")
		return
	}
	if err := s.DB.SetMapBackground(ctx, m.ID, data, mediaType, cfg.Width, cfg.Height); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "map.background", m.Name, fmt.Sprintf("%s %dx%d %d bytes", format, cfg.Width, cfg.Height, len(data)))
	redirect(w, r, fmt.Sprintf("/maps/%d?edit=1", m.ID))
}

// mapNodes replaces the switches and internet connections on a map (JSON from map.js).
func (s *Server) mapNodes(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMap(w, r)
	if !ok {
		return
	}
	var in []struct {
		Kind string  `json:"kind"`
		ID   int64   `json:"id"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || len(in) > 2000 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var nodes []store.MapNode
	var wan []store.MapWANNode
	seen := map[string]bool{}
	for _, n := range in {
		key := n.Kind + strconv.FormatInt(n.ID, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		switch n.Kind {
		case "switch":
			nodes = append(nodes, store.MapNode{SwitchID: n.ID, X: n.X, Y: n.Y})
		case "wan":
			wan = append(wan, store.MapWANNode{WANLinkID: n.ID, X: n.X, Y: n.Y})
		default:
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}
	if err := s.DB.SetMapNodes(r.Context(), m.ID, nodes, wan); err != nil {
		http.Error(w, "unknown switch or WAN link", http.StatusUnprocessableEntity)
		return
	}
	s.Audit(r, "map.nodes", m.Name, fmt.Sprintf("%d switches, %d WAN links", len(nodes), len(wan)))
	w.WriteHeader(http.StatusNoContent)
}

// mapLinkAdd stores a manual link or hides an automatic one.
func (s *Server) mapLinkAdd(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMap(w, r)
	if !ok {
		return
	}
	num := func(k string) int64 { v, _ := strconv.ParseInt(r.PostFormValue(k), 10, 64); return v }
	l := &store.MapLink{Kind: r.PostFormValue("kind"), ASwitch: num("a_switch"), AIf: int(num("a_if")),
		BSwitch: num("b_switch"), BIf: int(num("b_if"))}
	if err := s.DB.AddMapLink(r.Context(), m.ID, l); err != nil {
		http.Error(w, "invalid link", http.StatusUnprocessableEntity)
		return
	}
	s.Audit(r, "map.link."+l.Kind, m.Name, fmt.Sprintf("%d/%d–%d/%d", l.ASwitch, l.AIf, l.BSwitch, l.BIf))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mapLinkDelete(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMap(w, r)
	if !ok {
		return
	}
	lid, _ := strconv.ParseInt(chi.URLParam(r, "lid"), 10, 64)
	if err := s.DB.DeleteMapLink(r.Context(), m.ID, lid); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.Audit(r, "map.link.delete", m.Name, strconv.FormatInt(lid, 10))
	w.WriteHeader(http.StatusNoContent)
}
