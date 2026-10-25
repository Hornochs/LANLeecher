package web

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/accounting"
	"github.com/lan-dot-party/lanleecher/internal/fdb"
	"github.com/lan-dot-party/lanleecher/internal/scheduler"
	"github.com/lan-dot-party/lanleecher/internal/snmp"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

type switchRow struct {
	Switch   *store.Switch
	Ports    int
	Suspects int
	Status   scheduler.Status
}

func (s *Server) switches(w http.ResponseWriter, r *http.Request) {
	list, err := s.DB.ListSwitches(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var rows []switchRow
	for _, sw := range list {
		ports, err := s.DB.ListPorts(r.Context(), sw.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		row := switchRow{Switch: sw, Status: s.Sched.Status(sw.ID)}
		for _, p := range ports {
			if p.Physical {
				row.Ports++
			}
			if p.UplinkSuspect && p.Role != accounting.RoleUplink {
				row.Suspects++
			}
		}
		rows = append(rows, row)
	}
	s.render(w, r, "switches", s.page(r, translate(language(r), "nav.switches"), "switches", struct{ Rows []switchRow }{rows}))
}

type switchFormData struct {
	Switch          *store.Switch
	Creds           snmp.Credentials
	Versions        []string
	Levels          []string
	AuthProtos      []string
	PrivProtos      []string
	Adapters        []string
	DefaultInterval time.Duration
}

func (s *Server) switchForm(w http.ResponseWriter, r *http.Request, sw *store.Switch, creds snmp.Credentials) {
	if creds.SecurityLevel == "" {
		creds.SecurityLevel = "authPriv"
	}
	if creds.AuthProtocol == "" {
		creds.AuthProtocol = "SHA-256"
	}
	if creds.PrivProtocol == "" {
		creds.PrivProtocol = "AES-128"
	}
	// Secrets are never sent back to the browser.
	creds.Community, creds.AuthPassword, creds.PrivPassword = "", "", ""
	d := switchFormData{Switch: sw, Creds: creds, Versions: snmp.Versions, Levels: snmp.SecurityLevels,
		AuthProtos: snmp.AuthProtocols, PrivProtos: snmp.PrivProtocols, Adapters: fdb.Adapters,
		DefaultInterval: s.Cfg.Polling.Interval}
	s.render(w, r, "switch_form", s.page(r, translate(language(r), "switches.add"), "switches", d))
}

func (s *Server) switchNew(w http.ResponseWriter, r *http.Request) {
	s.switchForm(w, r, &store.Switch{Port: 161, SNMPVersion: "v2c", FDBAdapter: "auto", Enabled: true}, snmp.Credentials{})
}

func (s *Server) switchEdit(w http.ResponseWriter, r *http.Request) {
	sw, creds, ok := s.loadSwitch(w, r)
	if !ok {
		return
	}
	s.switchForm(w, r, sw, creds)
}

func (s *Server) loadSwitch(w http.ResponseWriter, r *http.Request) (*store.Switch, snmp.Credentials, bool) {
	id, ok := pathID(r)
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "error.not_found")
		return nil, snmp.Credentials{}, false
	}
	sw, err := s.DB.GetSwitch(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return nil, snmp.Credentials{}, false
	}
	plain, err := s.Key.Decrypt(sw.CredentialsEnc)
	if err != nil {
		s.fail(w, r, err)
		return nil, snmp.Credentials{}, false
	}
	creds, _ := snmp.UnmarshalCredentials(plain)
	return sw, creds, true
}

// parseSwitchForm reads the form. Empty secret fields keep the stored values.
func (s *Server) parseSwitchForm(r *http.Request, sw *store.Switch, old snmp.Credentials) (snmp.Credentials, error) {
	sw.Name = strings.TrimSpace(r.PostFormValue("name"))
	sw.Host = strings.TrimSpace(r.PostFormValue("host"))
	sw.Port, _ = strconv.Atoi(r.PostFormValue("port"))
	if sw.Port == 0 {
		sw.Port = 161
	}
	sw.SNMPVersion = r.PostFormValue("snmp_version")
	sw.PollInterval, _ = strconv.Atoi(r.PostFormValue("poll_interval"))
	sw.FDBAdapter = r.PostFormValue("fdb_adapter")
	sw.ARPSource = r.PostFormValue("arp_source") == "1"
	sw.Enabled = r.PostFormValue("enabled") == "1"

	creds := snmp.Credentials{
		Community:     r.PostFormValue("community"),
		Username:      strings.TrimSpace(r.PostFormValue("v3_user")),
		SecurityLevel: r.PostFormValue("v3_level"),
		AuthProtocol:  r.PostFormValue("v3_auth"),
		AuthPassword:  r.PostFormValue("v3_auth_pass"),
		PrivProtocol:  r.PostFormValue("v3_priv"),
		PrivPassword:  r.PostFormValue("v3_priv_pass"),
		ContextName:   strings.TrimSpace(r.PostFormValue("v3_context")),
	}
	if creds.Community == "" {
		creds.Community = old.Community
	}
	if creds.AuthPassword == "" {
		creds.AuthPassword = old.AuthPassword
	}
	if creds.PrivPassword == "" {
		creds.PrivPassword = old.PrivPassword
	}
	if sw.SNMPVersion == "v2c" {
		creds = snmp.Credentials{Community: creds.Community}
	} else {
		creds.Community = ""
	}

	switch {
	case sw.Name == "" || len(sw.Name) > 60:
		return creds, errors.New("name")
	case sw.Port < 1 || sw.Port > 65535:
		return creds, errors.New("port")
	case sw.PollInterval != 0 && (sw.PollInterval < 5 || sw.PollInterval > 3600):
		return creds, errors.New("interval")
	}
	if _, err := fdb.ForAdapter(sw.FDBAdapter); err != nil {
		return creds, err
	}
	if net.ParseIP(sw.Host) == nil && !validHostname(sw.Host) {
		return creds, errors.New("host")
	}
	return creds, snmp.Target{Host: sw.Host, Port: sw.Port, Version: sw.SNMPVersion, Creds: creds}.Validate()
}

func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, r := range h {
		if !(r == '-' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func (s *Server) saveSwitch(w http.ResponseWriter, r *http.Request, sw *store.Switch, old snmp.Credentials) {
	creds, err := s.parseSwitchForm(r, sw, old)
	if err != nil {
		s.renderSwitchError(w, r, sw, creds, err)
		return
	}
	plain, _ := creds.Marshal()
	if sw.CredentialsEnc, err = s.Key.Encrypt(plain); err != nil {
		s.fail(w, r, err)
		return
	}
	action := "switch.update"
	if sw.ID == 0 {
		action = "switch.create"
		err = s.DB.CreateSwitch(r.Context(), sw)
	} else {
		err = s.DB.UpdateSwitch(r.Context(), sw)
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			s.renderSwitchError(w, r, sw, creds, errors.New("duplicate"))
			return
		}
		s.fail(w, r, err)
		return
	}
	// Never log credentials.
	s.Audit(r, action, sw.Name, sw.Host+" "+sw.SNMPVersion)
	if err := s.Sched.Reload(r.Context()); err != nil {
		s.Log.Error("reload pollers", "err", err)
	}
	redirect(w, r, "/switches/"+strconv.FormatInt(sw.ID, 10)+"?ok=saved")
}

func (s *Server) renderSwitchError(w http.ResponseWriter, r *http.Request, sw *store.Switch, creds snmp.Credentials, err error) {
	w.WriteHeader(http.StatusBadRequest)
	key := "error.switch_invalid"
	switch err.Error() {
	case "duplicate":
		key = "error.switch_duplicate"
	case "host":
		key = "error.switch_host"
	}
	// Re-render the form with the error; switchForm writes the body.
	r2 := r.Clone(r.Context())
	q := r2.URL.Query()
	q.Set("err", strings.TrimPrefix(key, "error."))
	r2.URL.RawQuery = q.Encode()
	s.switchForm(&statusOverride{ResponseWriter: w}, r2, sw, creds)
}

// statusOverride swallows the second WriteHeader from render.
type statusOverride struct{ http.ResponseWriter }

func (o *statusOverride) WriteHeader(int) {}

func (s *Server) switchCreate(w http.ResponseWriter, r *http.Request) {
	s.saveSwitch(w, r, &store.Switch{}, snmp.Credentials{})
}

func (s *Server) switchUpdate(w http.ResponseWriter, r *http.Request) {
	sw, creds, ok := s.loadSwitch(w, r)
	if !ok {
		return
	}
	s.saveSwitch(w, r, sw, creds)
}

func (s *Server) switchDelete(w http.ResponseWriter, r *http.Request) {
	sw, _, ok := s.loadSwitch(w, r)
	if !ok {
		return
	}
	if err := s.DB.DeleteSwitch(r.Context(), sw.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "switch.delete", sw.Name, sw.Host)
	if err := s.Sched.Reload(r.Context()); err != nil {
		s.Log.Error("reload pollers", "err", err)
	}
	redirect(w, r, "/switches?ok=deleted")
}

// switchTest runs "Verbindung testen" with the form values (htmx partial).
func (s *Server) switchTest(w http.ResponseWriter, r *http.Request) {
	var old snmp.Credentials
	sw := &store.Switch{}
	if id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64); id > 0 {
		if existing, err := s.DB.GetSwitch(r.Context(), id); err == nil {
			if plain, err := s.Key.Decrypt(existing.CredentialsEnc); err == nil {
				old, _ = snmp.UnmarshalCredentials(plain)
			}
		}
	}
	creds, err := s.parseSwitchForm(r, sw, old)
	p := s.page(r, "", "switches", nil)
	if err != nil && err.Error() != "name" {
		s.errorPage(w, r, http.StatusBadRequest, "error.switch_invalid")
		return
	}
	t := snmp.Target{Host: sw.Host, Port: sw.Port, Version: sw.SNMPVersion, Creds: creds,
		Timeout: s.Cfg.Polling.Timeout, Retries: s.Cfg.Polling.Retries}
	res, err := s.Sched.Test(r.Context(), t)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<section class="card test-fail"><h3>✗ ` + p.T("switches.test_fail") + `</h3><p class="error">` +
			htmlEscape(err.Error()) + `</p><p class="muted small">` + p.T("switches.test_fail_hint") + `</p></section>`))
		return
	}
	p.Data = res
	s.partial(w, r, "switch_form", "switch_test", p)
}

type portRow struct {
	Port *store.Port
	// IsGroup is true when the port's traffic goes to a port group, which
	// is named after the port label in rankings and on the board.
	IsGroup    bool
	RoleSelect roleSelect
	MACs       []store.MACInfo
	Traffic    store.PortTraffic
	Rate       scheduler.Rate
	HasRate    bool
}

type roleSelect struct {
	P       *Page
	Name    string
	Roles   []accounting.Role
	Current accounting.Role
	Keep    bool
}

func (s *Server) ports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sw, _, ok := s.loadSwitch(w, r)
	if !ok {
		return
	}
	ports, err := s.DB.ListPorts(ctx, sw.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cutoff := time.Now().Add(-3 * s.Cfg.Polling.FDBInterval)
	current, err := s.DB.CurrentMACs(ctx, sw.ID, cutoff)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var allMACs []string
	for _, macs := range current {
		allMACs = append(allMACs, macs...)
	}
	infos, err := s.DB.MACInfos(ctx, allMACs)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	traffic := map[int64]store.PortTraffic{}
	if ev, err := s.DB.ActiveEvent(ctx); err == nil {
		traffic, _ = s.DB.PortTrafficBySwitch(ctx, ev.ID, sw.ID)
	}
	rates := s.Sched.PortRates(sw.ID)
	showAll := r.URL.Query().Get("all") == "1"
	p := s.page(r, sw.Name, "switches", nil)
	data := struct {
		Switch       *store.Switch
		Rows         []portRow
		ShowAll      bool
		VirtualCount int
		Suspects     int
		BulkRole     roleSelect
	}{Switch: sw, ShowAll: showAll, BulkRole: roleSelect{P: p, Name: "bulk_role", Roles: accounting.Roles, Keep: true}}
	for _, port := range ports {
		if !port.Physical {
			data.VirtualCount++
			if !showAll {
				continue
			}
		}
		if port.UplinkSuspect && port.Role != accounting.RoleUplink {
			data.Suspects++
		}
		row := portRow{Port: port, Traffic: traffic[port.ID],
			RoleSelect: roleSelect{P: p, Name: "role_" + strconv.FormatInt(port.ID, 10), Roles: accounting.Roles, Current: port.Role}}
		if port.Role != accounting.RoleUplink {
			for _, mac := range current[port.ID] {
				if info, ok := infos[mac]; ok {
					row.MACs = append(row.MACs, info)
				}
			}
		}
		row.IsGroup = port.Role == accounting.RoleMultiClient ||
			(port.Role != accounting.RoleUplink && len(row.MACs) > 1)
		if rate, ok := rates[port.ID]; ok {
			row.Rate, row.HasRate = rate, true
		}
		data.Rows = append(data.Rows, row)
	}
	p.Data = data
	s.render(w, r, "ports", p)
}

// portsUpdate saves per-row settings, or applies bulk settings to selected ports.
func (s *Server) portsUpdate(w http.ResponseWriter, r *http.Request) {
	sw, _, ok := s.loadSwitch(w, r)
	if !ok {
		return
	}
	ports, err := s.DB.ListPorts(r.Context(), sw.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byID := map[int64]*store.Port{}
	for _, p := range ports {
		byID[p.ID] = p
	}
	if err := r.ParseForm(); err != nil {
		s.errorPage(w, r, http.StatusBadRequest, "error.bad_request")
		return
	}
	changed := 0
	if r.PostFormValue("action") == "bulk" {
		var ids []int64
		for _, v := range r.PostForm["sel"] {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil && byID[id] != nil {
				ids = append(ids, id)
			}
		}
		var settings store.PortSettings
		if role := accounting.Role(r.PostFormValue("bulk_role")); role.Valid() {
			settings.Role = &role
		}
		switch r.PostFormValue("bulk_hidden") {
		case "1":
			t := true
			settings.Hidden = &t
		case "0":
			f := false
			settings.Hidden = &f
		}
		switch r.PostFormValue("bulk_uplink") {
		case "dismiss":
			t := true
			settings.UplinkDismissed = &t
		case "restore":
			f := false
			settings.UplinkDismissed = &f
		}
		settings.UplinkThreshold = s.Cfg.Polling.UplinkMACThreshold
		if len(ids) > 0 && (settings.Role != nil || settings.Hidden != nil || settings.UplinkDismissed != nil) {
			if err := s.DB.UpdatePorts(r.Context(), ids, settings); err != nil {
				s.fail(w, r, err)
				return
			}
			changed = len(ids)
		}
	} else {
		for _, v := range r.PostForm["port_ids"] {
			id, err := strconv.ParseInt(v, 10, 64)
			port := byID[id]
			if err != nil || port == nil {
				continue
			}
			key := strconv.FormatInt(id, 10)
			var settings store.PortSettings
			role := accounting.Role(r.PostFormValue("role_" + key))
			hidden := r.PostFormValue("hidden_"+key) == "1"
			label := strings.TrimSpace(r.PostFormValue("label_" + key))
			if len(label) > 60 {
				label = label[:60]
			}
			if role.Valid() && role != port.Role {
				settings.Role = &role
				// A role change applies the role's default visibility unless
				// the checkbox was changed at the same time.
				if hidden != port.Hidden {
					settings.Hidden = &hidden
				}
			} else if hidden != port.Hidden {
				settings.Hidden = &hidden
			}
			if label != port.Label {
				settings.Label = &label
			}
			// The "not an uplink" and "check again" buttons of a row.
			switch r.PostFormValue("uplink_hint") {
			case "dismiss:" + key:
				t := true
				settings.UplinkDismissed = &t
			case "restore:" + key:
				f := false
				settings.UplinkDismissed = &f
			}
			settings.UplinkThreshold = s.Cfg.Polling.UplinkMACThreshold
			if settings.Role == nil && settings.Hidden == nil && settings.Label == nil && settings.UplinkDismissed == nil {
				continue
			}
			if err := s.DB.UpdatePorts(r.Context(), []int64{id}, settings); err != nil {
				s.fail(w, r, err)
				return
			}
			changed++
		}
	}
	if changed > 0 {
		s.Audit(r, "ports.update", sw.Name, strconv.Itoa(changed)+" ports")
	}
	if action, id, ok := strings.Cut(r.PostFormValue("uplink_hint"), ":"); ok && (action == "dismiss" || action == "restore") {
		if n, err := strconv.ParseInt(id, 10, 64); err == nil && byID[n] != nil {
			s.Audit(r, "ports.uplink_"+action, sw.Name, byID[n].ShortName())
		}
	}
	back := "/switches/" + strconv.FormatInt(sw.ID, 10) + "?ok=saved"
	if r.PostFormValue("all") == "1" {
		back += "&all=1"
	}
	redirect(w, r, back)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;").Replace(s)
}
