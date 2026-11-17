package web

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/agentpool"
	"github.com/lan-dot-party/lanleecher/internal/app"
	"github.com/lan-dot-party/lanleecher/internal/auth"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

// agentStaleAfter is how long an agent may stay silent before it shows as stale.
const agentStaleAfter = 2 * time.Minute

type agentMAC struct {
	agentpool.MACStatus
	PortName string
}

type agentRow struct {
	*store.Agent
	State string // active | stale | revoked
	MACs  []agentMAC
}

type agentsData struct {
	Agents    []agentRow
	NewConfig string
	TLSNote   bool
}

func (s *Server) agentsPage(w http.ResponseWriter, r *http.Request, newConfig string, tlsNote bool) {
	ctx := r.Context()
	list, err := s.DB.ListAgents(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	now := store.Now()
	rows := make([]agentRow, 0, len(list))
	for _, a := range list {
		row := agentRow{Agent: a, State: "stale"}
		switch {
		case a.Token.Revoked:
			row.State = "revoked"
		case a.LastReport != nil && now.Sub(*a.LastReport) < agentStaleAfter:
			row.State = "active"
		}
		if !a.Token.Revoked && s.Agents != nil {
			for _, m := range s.Agents.Status(a.Token.ID, now) {
				am := agentMAC{MACStatus: m}
				if m.PortID != 0 {
					if p, err := s.DB.GetPort(ctx, m.PortID); err == nil {
						am.PortName = p.SwitchName + " " + p.ShortName()
					}
				}
				row.MACs = append(row.MACs, am)
			}
		}
		rows = append(rows, row)
	}
	s.render(w, r, "agents", s.page(r, translate(language(r), "admin.agents"), "admin",
		agentsData{Agents: rows, NewConfig: newConfig, TLSNote: tlsNote}))
}

func (s *Server) agents(w http.ResponseWriter, r *http.Request) { s.agentsPage(w, r, "", false) }

func (s *Server) agentCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.errorPage(w, r, http.StatusBadRequest, "error.bad_request")
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	var ips []string
	for _, ip := range strings.Fields(strings.ReplaceAll(r.PostFormValue("allowed_ips"), ",", " ")) {
		if _, err := netip.ParsePrefix(ip); err == nil {
			ips = append(ips, ip)
		} else if _, err := netip.ParseAddr(ip); err == nil {
			ips = append(ips, ip)
		} else {
			redirect(w, r, "/admin/agents?err=bad_ip")
			return
		}
	}
	if name == "" || len(name) > 60 {
		redirect(w, r, "/admin/agents?err=agent_invalid")
		return
	}
	plain := auth.NewAPIToken()
	t := &store.APIToken{Name: name, TokenHash: auth.HashToken(plain), Prefix: plain[:10],
		Scopes: []string{auth.ScopeAgentReport}, AllowedIPs: ips, CreatedBy: app.User(r.Context()).ID}
	if days, _ := strconv.Atoi(r.PostFormValue("expires_days")); days > 0 {
		exp := store.Now().Add(time.Duration(days) * 24 * time.Hour)
		t.ExpiresAt = &exp
	}
	if err := s.DB.CreateAgent(r.Context(), t); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "agent.create", name, "")

	var b strings.Builder
	b.WriteString("# /etc/lanleecher/agent.conf\nserver: " + s.Cfg.PublicURL + "\ntoken: " + plain + "\ninterval: 15s\nmode: auto\n")
	tlsNote := false
	if pin := s.certPin(); pin != "" {
		b.WriteString("tls:\n  pinned_sha256: " + pin + "\n")
		tlsNote = true
	}
	// The plaintext token is shown exactly once, in this response.
	s.agentsPage(w, r, b.String(), tlsNote)
}

// certPin returns the lowercase hex SHA-256 of the first certificate's DER in
// the configured TLS certificate file, or "" when TLS is off or unreadable.
func (s *Server) certPin() string {
	if s.Cfg.TLS.CertFile == "" {
		return ""
	}
	data, err := os.ReadFile(s.Cfg.TLS.CertFile)
	if err != nil {
		return ""
	}
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return ""
		}
		if block.Type == "CERTIFICATE" {
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				return ""
			}
			sum := sha256.Sum256(block.Bytes)
			return hex.EncodeToString(sum[:])
		}
	}
}

func (s *Server) agentRevoke(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "error.not_found")
		return
	}
	isAgent, err := s.DB.IsAgentToken(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !isAgent {
		s.errorPage(w, r, http.StatusNotFound, "error.not_found")
		return
	}
	if err := s.DB.RevokeAPIToken(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Agents != nil {
		s.Agents.Forget(id)
	}
	s.Audit(r, "agent.revoke", strconv.FormatInt(id, 10), "")
	redirect(w, r, "/admin/agents?ok=revoked")
}
