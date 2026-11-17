package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/app"
	"github.com/lan-dot-party/lanleecher/internal/auth"
	"github.com/lan-dot-party/lanleecher/internal/config"
	"github.com/lan-dot-party/lanleecher/internal/scheduler"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

type harness struct {
	t       *testing.T
	db      *store.DB
	dbPath  string
	srv     *httptest.Server
	client  *http.Client
	csrf    string
	current string
	cookies map[string]string // username -> session token
	tokens  map[string]string // username -> CSRF token
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "web.db")
	db, err := store.Open(store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := db.Migrate(context.Background(), log); err != nil {
		t.Fatal(err)
	}
	a := app.New(config.Default(), db, nil, log)
	ui, err := New(a)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.SessionMiddleware(ui.Routes()))
	t.Cleanup(srv.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &harness{t: t, db: db, dbPath: dbPath, srv: srv, client: client, cookies: map[string]string{}, tokens: map[string]string{}}
}

// user creates a user with a session; login selects it.
func (h *harness) user(name, role string) {
	h.t.Helper()
	ctx := context.Background()
	u := &store.User{Username: name, PasswordHash: "x", Role: role}
	if err := h.db.CreateUser(ctx, u); err != nil {
		h.t.Fatal(err)
	}
	token, csrf := auth.RandomToken(16), auth.RandomToken(16)
	if err := h.db.CreateSession(ctx, &store.Session{ID: auth.HashToken(token), UserID: u.ID, CSRFToken: csrf,
		ExpiresAt: store.Now().Add(time.Hour)}); err != nil {
		h.t.Fatal(err)
	}
	h.cookies[name], h.tokens[name] = token, csrf
}

func (h *harness) login(name string) {
	h.t.Helper()
	h.current, h.csrf = name, h.tokens[name]
}

func (h *harness) send(method, path string, body io.Reader, contentType string, csrf bool) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if csrf {
		req.Header.Set("X-CSRF-Token", h.csrf)
	}
	req.AddCookie(&http.Cookie{Name: app.SessionCookie, Value: h.cookies[h.current]})
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

func (h *harness) get(path string) (*http.Response, string) {
	h.t.Helper()
	resp := h.send(http.MethodGet, path, nil, "", false)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// post sends a form with the CSRF token as header (nocsrf omits it).
func (h *harness) post(path string, form url.Values, csrf bool) *http.Response {
	h.t.Helper()
	resp := h.send(http.MethodPost, path, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", csrf)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func (h *harness) postJSON(path, body string, csrf bool) *http.Response {
	h.t.Helper()
	resp := h.send(http.MethodPost, path, strings.NewReader(body), "application/json", csrf)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func (h *harness) upload(path, field, name string, data []byte) *http.Response {
	h.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("_csrf", h.csrf)
	fw, _ := mw.CreateFormFile(field, name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	resp := h.send(http.MethodPost, path, &body, mw.FormDataContentType(), false)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func pngBytes(t *testing.T, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestMaps(t *testing.T) {
	h := newHarness(t)
	h.user("admin", store.RoleAdmin)
	h.user("bob", store.RoleViewer)
	ctx := context.Background()
	mk := func(name string) *store.Switch {
		sw := &store.Switch{Name: name, Host: "192.0.2.2", Port: 161, SNMPVersion: "v2c", CredentialsEnc: []byte{0},
			FDBAdapter: "auto", Enabled: true}
		if err := h.db.CreateSwitch(ctx, sw); err != nil {
			t.Fatal(err)
		}
		return sw
	}
	a, b, c := mk("core"), mk("access"), mk("spare")
	// core port 49 <-> access port 1 over LLDP; c is archived (imported).
	if err := h.db.InTx(ctx, func(tx *store.Tx) error {
		for _, sw := range []struct {
			id     int64
			ifName string
			ifIdx  int
			mac    string
			nb     store.LLDPNeighbor
		}{
			{a.ID, "1/49", 49, "02:00:00:00:00:0a", store.LLDPNeighbor{IfIndex: 49, ChassisID: "02:00:00:00:00:0b", SysName: "access", PortID: "1"}},
			{b.ID, "1", 1, "02:00:00:00:00:0b", store.LLDPNeighbor{IfIndex: 1, ChassisID: "02:00:00:00:00:0a", SysName: "core", PortID: "1/49"}},
		} {
			if _, err := tx.UpsertPorts(ctx, sw.id, []store.PortObservation{{IfIndex: sw.ifIdx, IfName: sw.ifName, Physical: true,
				OperUp: true, SpeedMbps: 1000}}, store.Now()); err != nil {
				return err
			}
			if err := tx.RecordTopology(ctx, sw.id, sw.mac, "", map[int]store.LLDPNeighbor{sw.ifIdx: sw.nb}, map[int]int{}, store.Now()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Archived switches come from an import; mark one directly (the sqlite driver is registered by the store).
	raw, err := sql.Open("sqlite", "file:"+h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE switches SET archived = 1 WHERE id = ?`, c.ID); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	wan := &store.WANLink{Name: "Internet <b>"}
	if err := h.db.CreateWANLink(ctx, wan); err != nil {
		t.Fatal(err)
	}

	// Viewers see the map page but cannot create or change anything.
	h.login("bob")
	if resp, _ := h.get("/maps"); resp.StatusCode != 200 {
		t.Fatalf("maps page for viewers: %d", resp.StatusCode)
	}
	if resp := h.post("/maps", url.Values{"name": {"x"}}, true); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer created a map: %d", resp.StatusCode)
	}

	h.login("admin")
	if resp := h.post("/maps", url.Values{"name": {"Hall 1"}}, false); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("create without CSRF token: %d", resp.StatusCode)
	}
	if resp := h.post("/maps", url.Values{"name": {"Hall 1"}}, true); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	maps, _ := h.db.ListMaps(ctx)
	if len(maps) != 1 {
		t.Fatalf("maps %v", maps)
	}
	m := maps[0]
	base := fmt.Sprintf("/maps/%d", m.ID)
	if resp, body := h.get("/maps"); resp.StatusCode != http.StatusSeeOther || body != "" && !strings.Contains(body, base) {
		t.Errorf("/maps redirect: %d", resp.StatusCode)
	}
	if _, body := h.get(base); !strings.Contains(body, "/static/map.js") || strings.Contains(body, "style=") {
		t.Error("map page: script missing or inline style")
	}

	// Background: PNG accepted, SVG and junk refused.
	if r := h.upload(base+"/background", "image", "plan.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("svg accepted: %d", r.StatusCode)
	}
	big := append(pngBytes(t, 10, 10), make([]byte, 9<<20)...)
	if r := h.upload(base+"/background", "image", "big.png", big); r.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("9 MiB image: %d", r.StatusCode)
	}
	if r := h.upload(base+"/background", "image", "x.png", []byte("not an image")); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("junk accepted: %d", r.StatusCode)
	}
	if r := h.upload(base+"/background", "image", "plan.png", pngBytes(t, 800, 500)); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("png refused: %d", r.StatusCode)
	}
	r, body := h.get(base + "/background")
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/png" || r.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatalf("background: %d %v", r.StatusCode, r.Header)
	}
	// A large non-multipart body is refused.
	if r := h.send(http.MethodPost, base+"/nodes", strings.NewReader(strings.Repeat(" ", 17<<20)), "application/json", true); r.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("17 MiB body: %d", r.StatusCode)
	}

	state := func() mapStateJSON {
		t.Helper()
		_, body := h.get(base + "/state.json")
		var st mapStateJSON
		if err := json.Unmarshal([]byte(body), &st); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		return st
	}
	// An empty map has empty arrays, not null (map.js iterates them).
	if _, body := h.get(base + "/state.json"); !strings.Contains(body, `"nodes":[]`) || !strings.Contains(body, `"links":[]`) {
		t.Errorf("empty map: %s", body)
	}
	nodes := fmt.Sprintf(`[{"kind":"switch","id":%d,"x":0.2,"y":0.5},{"kind":"switch","id":%d,"x":0.8,"y":0.5},{"kind":"switch","id":%d,"x":0.5,"y":0.9},{"kind":"wan","id":%d,"x":0.5,"y":0.1}]`,
		a.ID, b.ID, c.ID, wan.ID)
	if r := h.postJSON(base+"/nodes", nodes, false); r.StatusCode != http.StatusForbidden {
		t.Fatalf("nodes without CSRF token: %d", r.StatusCode)
	}
	if r := h.postJSON(base+"/nodes", `[{"kind":"switch","id":9999,"x":0,"y":0}]`, true); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown switch: %d", r.StatusCode)
	}
	if r := h.postJSON(base+"/nodes", nodes, true); r.StatusCode != http.StatusNoContent {
		t.Fatalf("nodes: %d", r.StatusCode)
	}
	st := state()
	if st.Map.Width != 800 || st.Map.Height != 500 || st.Map.BG == "" || len(st.Nodes) != 4 {
		t.Fatalf("state %+v", st)
	}
	status := map[string]string{}
	for _, n := range st.Nodes {
		status[n.Key] = n.Status
		if n.Kind == "switch" && n.Href != fmt.Sprintf("/switches/%d", n.ID) {
			t.Errorf("href %q", n.Href)
		}
	}
	if status[switchKey(c.ID)] != "archived" || status[switchKey(a.ID)] != "unknown" || status[wanKey(wan.ID)] != "wan" {
		t.Errorf("status %v", status)
	}
	if len(st.Links) != 1 || st.Links[0].Source != "lldp" {
		t.Fatalf("links %+v", st.Links)
	}
	l := st.Links[0]
	// Hide the automatic link, add a manual one to the archived switch.
	if r := h.post(base+"/links", url.Values{"kind": {"hidden"}, "a_switch": {fmt.Sprint(l.A.Device)}, "a_if": {fmt.Sprint(l.A.IfIndex)},
		"b_switch": {fmt.Sprint(l.B.Device)}, "b_if": {fmt.Sprint(l.B.IfIndex)}}, true); r.StatusCode != http.StatusNoContent {
		t.Fatalf("hide: %d", r.StatusCode)
	}
	if r := h.post(base+"/links", url.Values{"kind": {"manual"}, "a_switch": {fmt.Sprint(a.ID)}, "a_if": {"49"},
		"b_switch": {fmt.Sprint(c.ID)}, "b_if": {"0"}}, true); r.StatusCode != http.StatusNoContent {
		t.Fatalf("manual: %d", r.StatusCode)
	}
	st = state()
	if len(st.Links) != 1 || st.Links[0].Source != "manual" || len(st.Hidden) != 1 {
		t.Fatalf("after hide/manual: links %+v hidden %+v", st.Links, st.Hidden)
	}
	if r := h.post(fmt.Sprintf("%s/links/%d/delete", base, st.Hidden[0].ID), url.Values{}, true); r.StatusCode != http.StatusNoContent {
		t.Fatalf("unhide: %d", r.StatusCode)
	}
	if st = state(); len(st.Links) != 2 {
		t.Fatalf("after unhide: %+v", st.Links)
	}
	// The admin gets the WAN node's link target, viewers do not; viewers get no editor data.
	if st.Nodes[len(st.Nodes)-1].Href != "/admin/wan" {
		t.Errorf("admin WAN href %q", st.Nodes[len(st.Nodes)-1].Href)
	}
	h.login("bob")
	st = state()
	if len(st.Links) != 2 || st.Available != nil || st.Hidden != nil || st.Nodes[0].Ports != nil || st.Nodes[len(st.Nodes)-1].Href != "" {
		t.Fatalf("viewer state %+v", st)
	}
	if r := h.postJSON(base+"/nodes", "[]", true); r.StatusCode != http.StatusForbidden {
		t.Errorf("viewer changed nodes: %d", r.StatusCode)
	}
}

func TestRates(t *testing.T) {
	end := func(known bool, out, in float64, speed uint64) mapEndJSON {
		return mapEndJSON{Known: known, OutBps: out, InBps: in, Speed: speed}
	}
	// From port A: out is A to B.
	l := rates(mapLinkJSON{A: end(true, 300e6, 100e6, 1_000_000_000), B: end(true, 1, 2, 1_000_000_000)})
	if !l.Rates || l.AB != 300e6 || l.BA != 100e6 || l.Util < 29.9 || l.Util > 30.1 {
		t.Errorf("A known: %+v", l)
	}
	// From port B, reversed: what B receives is A to B.
	l = rates(mapLinkJSON{A: end(false, 0, 0, 0), B: end(true, 100e6, 300e6, 1_000_000_000)})
	if !l.Rates || l.AB != 300e6 || l.BA != 100e6 || l.Util < 29.9 || l.Util > 30.1 {
		t.Errorf("B known: %+v", l)
	}
	if l = rates(mapLinkJSON{}); l.Rates {
		t.Errorf("nothing known: %+v", l)
	}
}

func TestWANLinks(t *testing.T) {
	p1 := &store.Port{ID: 1, SwitchID: 10, IfIndex: 1}
	p2 := &store.Port{ID: 2, SwitchID: 10, IfIndex: 2}
	p3 := &store.Port{ID: 3, SwitchID: 11, IfIndex: 1}
	live := map[int64]scheduler.Rate{
		// The router sends 400 Mbit/s into port 1 (stored as up) and receives 50 Mbit/s (stored as down).
		1: {DownBps: 50e6, UpBps: 400e6},
		2: {DownBps: 10e6, UpBps: 100e6},
	}
	end := func(sw int64, ifIndex int, port string) mapEndJSON {
		return mapEndJSON{Node: switchKey(sw), Device: sw, IfIndex: ifIndex, Speed: 1_000_000_000, Up: true}
	}
	onMap := map[int64]bool{10: true}
	// Booked bandwidth: utilisation of the sum against it; the switch 11 port is not on the map.
	got := wanLinks(&store.WANLink{ID: 5, Name: "Internet", DownMbps: 1000, UpMbps: 100, Ports: []*store.Port{p1, p2, p3}}, end, onMap, live)
	if len(got) != 2 || got[0].Source != "wan" || got[0].A.Node != "w5" || got[0].B.Node != "s10" {
		t.Fatalf("links %+v", got)
	}
	if got[0].AB != 400e6 || got[0].BA != 50e6 {
		t.Errorf("internet direction: down %v up %v", got[0].AB, got[0].BA)
	}
	// total down 500 of 1000, total up 60 of 100 -> 60 %.
	if got[0].Util < 59.9 || got[0].Util > 60.1 {
		t.Errorf("util against booked bandwidth = %v", got[0].Util)
	}
	// Without booked bandwidth: against the port speed (400 of 1000 Mbit/s).
	got = wanLinks(&store.WANLink{ID: 5, Name: "Internet", Ports: []*store.Port{p1}}, end, onMap, live)
	if got[0].Util < 39.9 || got[0].Util > 40.1 {
		t.Errorf("util against port speed = %v", got[0].Util)
	}
	// No live rate: the link is drawn without rates.
	got = wanLinks(&store.WANLink{ID: 5, Name: "Internet", Ports: []*store.Port{p3}}, end, map[int64]bool{11: true}, live)
	if len(got) != 1 || got[0].Rates {
		t.Errorf("no rate: %+v", got)
	}
}
