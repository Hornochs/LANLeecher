package web

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/kea"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

func (s *Server) dhcpList(w http.ResponseWriter, r *http.Request) {
	list, err := s.DB.ListDHCPSources(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "dhcp_list", s.page(r, translate(language(r), "admin.dhcp"), "admin", struct {
		Sources []*store.DHCPSource
	}{list}))
}

type dhcpFormData struct {
	Source      *store.DHCPSource
	Kinds       []kea.Kind
	HasPassword bool
	HasKey      bool
}

func (s *Server) dhcpForm(w http.ResponseWriter, r *http.Request, src *store.DHCPSource, sec kea.Secrets) {
	d := dhcpFormData{Source: src, Kinds: kea.Kinds, HasPassword: sec.Password != "", HasKey: sec.ClientKeyPEM != ""}
	s.render(w, r, "dhcp_form", s.page(r, translate(language(r), "admin.dhcp"), "admin", d))
}

func (s *Server) dhcpNew(w http.ResponseWriter, r *http.Request) {
	s.dhcpForm(w, r, &store.DHCPSource{Kind: string(kea.KindAgent), DHCP4: true, PollInterval: 30, Enabled: true}, kea.Secrets{})
}

func (s *Server) loadDHCPSource(w http.ResponseWriter, r *http.Request) (*store.DHCPSource, kea.Secrets, bool) {
	var sec kea.Secrets
	id, ok := pathID(r)
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "error.not_found")
		return nil, sec, false
	}
	src, err := s.DB.GetDHCPSource(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return nil, sec, false
	}
	if len(src.SecretsEnc) > 0 {
		plain, err := s.Key.Decrypt(src.SecretsEnc)
		if err != nil {
			s.fail(w, r, err)
			return nil, sec, false
		}
		if err := json.Unmarshal(plain, &sec); err != nil {
			s.fail(w, r, err)
			return nil, sec, false
		}
	}
	return src, sec, true
}

func (s *Server) dhcpEdit(w http.ResponseWriter, r *http.Request) {
	src, sec, ok := s.loadDHCPSource(w, r)
	if !ok {
		return
	}
	s.dhcpForm(w, r, src, sec)
}

// parseDHCPForm reads the form. Empty secret fields keep the stored values.
func parseDHCPForm(r *http.Request, src *store.DHCPSource, sec *kea.Secrets) string {
	src.Name = strings.TrimSpace(r.PostFormValue("name"))
	src.URL = strings.TrimSpace(r.PostFormValue("url"))
	src.Kind = r.PostFormValue("kind")
	src.DHCP4 = r.PostFormValue("dhcp4") == "1"
	src.DHCP6 = r.PostFormValue("dhcp6") == "1"
	src.Username = strings.TrimSpace(r.PostFormValue("username"))
	src.CAPEM = strings.TrimSpace(r.PostFormValue("ca_pem"))
	src.ClientCertPEM = strings.TrimSpace(r.PostFormValue("client_cert_pem"))
	src.InsecureSkipVerify = r.PostFormValue("insecure") == "1"
	src.Enabled = r.PostFormValue("enabled") == "1"
	src.PollInterval, _ = strconv.Atoi(r.PostFormValue("poll_interval"))
	if pw := r.PostFormValue("password"); pw != "" {
		sec.Password = pw
	}
	if r.PostFormValue("clear_password") == "1" {
		sec.Password = ""
	}
	if key := strings.TrimSpace(r.PostFormValue("client_key_pem")); key != "" {
		sec.ClientKeyPEM = key
	}
	if src.ClientCertPEM == "" {
		sec.ClientKeyPEM = ""
	}

	switch {
	case src.Name == "" || len(src.Name) > 60:
		return "dhcp_name"
	case !kea.Kind(src.Kind).Valid():
		return "bad_request"
	case src.Kind == string(kea.KindAgent) && !src.DHCP4 && !src.DHCP6:
		return "dhcp_family"
	case src.PollInterval < 10 || src.PollInterval > 3600:
		return "dhcp_interval"
	}
	if _, err := kea.ValidateURL(src.URL); err != nil {
		return "dhcp_url"
	}
	if src.CAPEM != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(src.CAPEM)) {
		return "dhcp_pem"
	}
	if src.ClientCertPEM != "" {
		if _, err := tls.X509KeyPair([]byte(src.ClientCertPEM), []byte(sec.ClientKeyPEM)); err != nil {
			return "dhcp_client_cert"
		}
	}
	return ""
}

func (s *Server) dhcpSave(w http.ResponseWriter, r *http.Request, src *store.DHCPSource, sec kea.Secrets) {
	if errKey := parseDHCPForm(r, src, &sec); errKey != "" {
		r2 := r.Clone(r.Context())
		q := r2.URL.Query()
		q.Set("err", errKey)
		r2.URL.RawQuery = q.Encode()
		w.WriteHeader(http.StatusBadRequest)
		s.dhcpForm(&statusOverride{ResponseWriter: w}, r2, src, sec)
		return
	}
	plain, _ := json.Marshal(sec)
	enc, err := s.Key.Encrypt(plain)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	src.SecretsEnc = enc
	action := "dhcp.update"
	if src.ID == 0 {
		action = "dhcp.create"
		err = s.DB.CreateDHCPSource(r.Context(), src)
	} else {
		err = s.DB.UpdateDHCPSource(r.Context(), src)
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			redirect(w, r, "/admin/dhcp?err=dhcp_duplicate")
			return
		}
		s.fail(w, r, err)
		return
	}
	details := src.URL + " " + src.Kind
	if src.InsecureSkipVerify {
		details += " INSECURE"
		s.Log.Warn("certificate verification disabled for DHCP source", "source", src.Name)
	}
	s.Audit(r, action, src.Name, details)
	s.reloadDHCP(r)
	redirect(w, r, "/admin/dhcp/"+strconv.FormatInt(src.ID, 10)+"?test=1")
}

func (s *Server) dhcpCreate(w http.ResponseWriter, r *http.Request) {
	s.dhcpSave(w, r, &store.DHCPSource{}, kea.Secrets{})
}

func (s *Server) dhcpUpdate(w http.ResponseWriter, r *http.Request) {
	src, sec, ok := s.loadDHCPSource(w, r)
	if !ok {
		return
	}
	s.dhcpSave(w, r, src, sec)
}

func (s *Server) dhcpDelete(w http.ResponseWriter, r *http.Request) {
	src, _, ok := s.loadDHCPSource(w, r)
	if !ok {
		return
	}
	if err := s.DB.DeleteDHCPSource(r.Context(), src.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "dhcp.delete", src.Name, src.URL)
	s.reloadDHCP(r)
	redirect(w, r, "/admin/dhcp?ok=deleted")
}

// certView is a certificate prepared for the template.
type certView struct {
	kea.CertInfo
	Hex string // raw fingerprint for the trust form
}

func viewCert(pemText string) *certView {
	if pemText == "" {
		return nil
	}
	c, err := kea.ParsePEM(pemText)
	if err != nil {
		return nil
	}
	return &certView{CertInfo: kea.Info(c, time.Now()), Hex: kea.Fingerprint(c)}
}

// certTableData and certWarningData are the dots of the certificate templates.
type certTableData struct {
	P *Page
	C *certView
}

type certWarningData struct {
	P              *Page
	C              *certView
	Source         *store.DHCPSource
	Problem        string
	Reason         string
	OldFingerprint string
}

func init() {
	funcs["certTable"] = func(p *Page, c *certView) certTableData { return certTableData{P: p, C: c} }
	funcs["certWarning"] = func(p *Page, src *store.DHCPSource, c *certView, problem, reason string) certWarningData {
		return certWarningData{P: p, C: c, Source: src, Problem: problem, Reason: reason,
			OldFingerprint: kea.FormatFingerprint(src.PinnedSHA256)}
	}
}

type dhcpSourceData struct {
	Source    *store.DHCPSource
	Pinned    *certView
	Presented *certView
	AutoTest  bool
}

func (s *Server) dhcpShow(w http.ResponseWriter, r *http.Request) {
	src, _, ok := s.loadDHCPSource(w, r)
	if !ok {
		return
	}
	d := dhcpSourceData{Source: src, Pinned: viewCert(src.PinnedCertPEM), AutoTest: r.URL.Query().Get("test") == "1"}
	if src.CertProblem != "" {
		d.Presented = viewCert(src.PresentedCertPEM)
	}
	s.render(w, r, "dhcp_source", s.page(r, src.Name, "admin", d))
}

type dhcpTestData struct {
	Source    *store.DHCPSource
	Result    kea.Result
	Presented *certView
	Families  []string
}

// dhcpTest runs "Test connection" (htmx partial). It is a full poll: leases of
// known MACs are recorded, and the certificate seen is stored for review.
func (s *Server) dhcpTest(w http.ResponseWriter, r *http.Request) {
	src, _, ok := s.loadDHCPSource(w, r)
	if !ok {
		return
	}
	res := s.DHCP.Poll(r.Context(), src)
	d := dhcpTestData{Source: src, Result: res}
	if res.CertErr != nil && res.CertErr.Cert != nil {
		d.Presented = &certView{CertInfo: kea.Info(res.CertErr.Cert, time.Now()), Hex: kea.Fingerprint(res.CertErr.Cert)}
	}
	for fam := range res.Leases {
		d.Families = append(d.Families, fam)
	}
	p := s.page(r, "", "admin", d)
	s.partial(w, r, "dhcp_source", "dhcp_test", p)
}

// dhcpTrust pins exactly the certificate the server presented last – like
// accepting a certificate in a browser. The fingerprint in the form must match
// the stored certificate, so a changed certificate is never trusted unseen.
func (s *Server) dhcpTrust(w http.ResponseWriter, r *http.Request) {
	src, _, ok := s.loadDHCPSource(w, r)
	if !ok {
		return
	}
	cert, err := kea.ParsePEM(src.PresentedCertPEM)
	fp := strings.ToLower(strings.TrimSpace(r.PostFormValue("fingerprint")))
	if err != nil || fp == "" || kea.Fingerprint(cert) != fp {
		redirect(w, r, "/admin/dhcp/"+strconv.FormatInt(src.ID, 10)+"?err=dhcp_cert_mismatch")
		return
	}
	if err := s.DB.PinDHCPCertificate(r.Context(), src.ID, fp, src.PresentedCertPEM); err != nil {
		s.fail(w, r, err)
		return
	}
	old := ""
	if src.PinnedSHA256 != "" {
		old = " replaces " + kea.FormatFingerprint(src.PinnedSHA256)
	}
	s.Audit(r, "dhcp.cert.trust", src.Name, "SHA-256 "+kea.FormatFingerprint(fp)+old)
	s.reloadDHCP(r)
	redirect(w, r, "/admin/dhcp/"+strconv.FormatInt(src.ID, 10)+"?ok=cert_trusted&test=1")
}

func (s *Server) dhcpUntrust(w http.ResponseWriter, r *http.Request) {
	src, _, ok := s.loadDHCPSource(w, r)
	if !ok {
		return
	}
	if err := s.DB.UnpinDHCPCertificate(r.Context(), src.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "dhcp.cert.revoke", src.Name, "SHA-256 "+kea.FormatFingerprint(src.PinnedSHA256))
	s.reloadDHCP(r)
	redirect(w, r, "/admin/dhcp/"+strconv.FormatInt(src.ID, 10)+"?ok=saved")
}

// dhcpProblems returns sources that need an admin's attention (dashboard).
func (s *Server) dhcpProblems(r *http.Request) []*store.DHCPSource {
	list, err := s.DB.ListDHCPSources(r.Context())
	if err != nil {
		return nil
	}
	var out []*store.DHCPSource
	for _, src := range list {
		if src.Enabled && (src.CertProblem != "" || (src.LastPollAt != nil && !src.LastPollOK)) {
			out = append(out, src)
		}
	}
	return out
}

// reloadDHCP restarts the DHCP pollers after a change; failures are logged.
func (s *Server) reloadDHCP(r *http.Request) {
	if err := s.DHCP.Reload(r.Context()); err != nil {
		s.Log.Error("reload dhcp sources", "err", err)
	}
}
