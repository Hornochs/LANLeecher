package web

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/app"
	"github.com/lan-dot-party/lanleecher/internal/auth"
	"github.com/lan-dot-party/lanleecher/internal/config"
	"github.com/lan-dot-party/lanleecher/internal/store"
	"github.com/lan-dot-party/lanleecher/pkg/version"
)

var userRoles = []string{store.RoleViewer, store.RoleOperator, store.RoleAdmin}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	users, err := s.DB.ListUsers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "users", s.page(r, translate(language(r), "admin.users"), "admin", struct {
		Users []*store.User
		Roles []string
	}{users, userRoles}))
}

func validUsername(u string) bool {
	if u == "" || len(u) > 40 {
		return false
	}
	for _, r := range u {
		if !(r == '.' || r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func (s *Server) userCreate(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PostFormValue("username"))
	if !validUsername(username) {
		redirect(w, r, "/admin/users?err=bad_request")
		return
	}
	hash, err := auth.HashPassword(r.PostFormValue("password"))
	if err != nil {
		redirect(w, r, "/admin/users?err=password_short")
		return
	}
	u := &store.User{Username: username, PasswordHash: hash, Role: r.PostFormValue("role"), Language: r.PostFormValue("language")}
	if err := s.DB.CreateUser(r.Context(), u); err != nil {
		redirect(w, r, "/admin/users?err=user_exists")
		return
	}
	s.Audit(r, "user.create", username, u.Role)
	redirect(w, r, "/admin/users?ok=saved")
}

// keepsAdmin refuses changes that would leave no enabled admin.
func (s *Server) keepsAdmin(r *http.Request, target *store.User, newRole string, disabled bool) bool {
	if target.Role != store.RoleAdmin || target.Disabled || (newRole == store.RoleAdmin && !disabled) {
		return true
	}
	n, err := s.DB.CountAdmins(r.Context())
	return err == nil && n > 1
}

func (s *Server) userUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	u, err := s.DB.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	role := r.PostFormValue("role")
	lang := r.PostFormValue("language")
	disabled := r.PostFormValue("disabled") == "1"
	if lang != "de" && lang != "en" {
		lang = "en"
	}
	if store.RoleLevel(role) == 0 {
		redirect(w, r, "/admin/users?err=bad_request")
		return
	}
	if !s.keepsAdmin(r, u, role, disabled) {
		redirect(w, r, "/admin/users?err=last_admin")
		return
	}
	if err := s.DB.UpdateUser(r.Context(), id, role, lang, disabled); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "user.update", u.Username, "role="+role+" disabled="+strconv.FormatBool(disabled))
	redirect(w, r, "/admin/users?ok=saved")
}

func (s *Server) userPassword(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	u, err := s.DB.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	hash, err := auth.HashPassword(r.PostFormValue("password"))
	if err != nil {
		redirect(w, r, "/admin/users?err=password_short")
		return
	}
	if err := s.DB.SetPassword(r.Context(), id, hash); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "user.password", u.Username, "by admin")
	if id == app.User(r.Context()).ID {
		redirect(w, r, "/login")
		return
	}
	redirect(w, r, "/admin/users?ok=saved")
}

func (s *Server) userDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	u, err := s.DB.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if id == app.User(r.Context()).ID || !s.keepsAdmin(r, u, "", true) {
		redirect(w, r, "/admin/users?err=last_admin")
		return
	}
	if err := s.DB.DeleteUser(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "user.delete", u.Username, "")
	redirect(w, r, "/admin/users?ok=deleted")
}

type tokensData struct {
	Tokens   []*store.APIToken
	Scopes   []string
	NewToken string
}

func (s *Server) tokensPage(w http.ResponseWriter, r *http.Request, newToken string) {
	tokens, err := s.DB.ListAPITokens(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "tokens", s.page(r, translate(language(r), "admin.tokens"), "admin",
		tokensData{Tokens: tokens, Scopes: auth.AllScopes, NewToken: newToken}))
}

func (s *Server) tokens(w http.ResponseWriter, r *http.Request) { s.tokensPage(w, r, "") }

func (s *Server) tokenCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.errorPage(w, r, http.StatusBadRequest, "error.bad_request")
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	var scopes []string
	for _, sc := range r.PostForm["scopes"] {
		if auth.ValidScope(sc) {
			scopes = append(scopes, sc)
		}
	}
	var ips []string
	for _, ip := range strings.Fields(strings.ReplaceAll(r.PostFormValue("allowed_ips"), ",", " ")) {
		if _, err := netip.ParsePrefix(ip); err == nil {
			ips = append(ips, ip)
		} else if _, err := netip.ParseAddr(ip); err == nil {
			ips = append(ips, ip)
		} else {
			redirect(w, r, "/admin/tokens?err=bad_ip")
			return
		}
	}
	if name == "" || len(name) > 60 || len(scopes) == 0 {
		redirect(w, r, "/admin/tokens?err=token_invalid")
		return
	}
	plain := auth.NewAPIToken()
	t := &store.APIToken{Name: name, TokenHash: auth.HashToken(plain), Prefix: plain[:10], Scopes: scopes, AllowedIPs: ips,
		CreatedBy: app.User(r.Context()).ID}
	if days, _ := strconv.Atoi(r.PostFormValue("expires_days")); days > 0 {
		exp := store.Now().Add(time.Duration(days) * 24 * time.Hour)
		t.ExpiresAt = &exp
	}
	if err := s.DB.CreateAPIToken(r.Context(), t); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "token.create", name, strings.Join(scopes, " "))
	// The plaintext is shown exactly once, in this response.
	s.tokensPage(w, r, plain)
}

func (s *Server) tokenRevoke(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "error.not_found")
		return
	}
	if err := s.DB.RevokeAPIToken(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "token.revoke", strconv.FormatInt(id, 10), "")
	redirect(w, r, "/admin/tokens?ok=revoked")
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.DB.ListAudit(r.Context(), 300)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "audit", s.page(r, translate(language(r), "admin.audit"), "admin", struct{ Entries []store.AuditEntry }{entries}))
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	v, _ := s.DB.SchemaVersion(r.Context())
	data := struct {
		Cfg           *config.Config
		StoragePath   string
		SchemaVersion int64
		Version       string
	}{s.Cfg, s.Cfg.ResolvePath(s.Cfg.Storage.Path), v, version.GetVersion()}
	s.render(w, r, "settings", s.page(r, translate(language(r), "admin.settings"), "admin", data))
}

// boardPage serves /board. It renders only a shell; board.js pulls the
// content from /api/v1/board(/stream).
func (s *Server) boardPage(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.Board.Enabled {
		s.errorPage(w, r, http.StatusNotFound, "error.not_found")
		return
	}
	if !s.Cfg.Board.Public && app.User(r.Context()) == nil {
		redirect(w, r, "/login?next=/board")
		return
	}
	limit := r.URL.Query().Get("limit")
	if limit != "20" && limit != "50" {
		limit = "10"
	}
	event := r.URL.Query().Get("event")
	if event != "" {
		if _, err := s.DB.EventBySlug(r.Context(), event); errors.Is(err, store.ErrNotFound) {
			event = ""
		}
	}
	s.render(w, r, "board", s.page(r, "Board", "", struct{ Limit, Event string }{limit, event}))
}
