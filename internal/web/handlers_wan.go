package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lan-dot-party/lanleecher/internal/store"
)

func (s *Server) wanPage(w http.ResponseWriter, r *http.Request) {
	links, err := s.DB.ListWANLinks(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "wan", s.page(r, translate(language(r), "admin.wan"), "admin",
		struct{ Links []*store.WANLink }{links}))
}

// wanFromForm reads a WAN link from the posted form. Unparseable or negative
// numbers are rejected by the store (ok is false only for non-numeric input).
func wanFromForm(r *http.Request) (*store.WANLink, bool) {
	l := &store.WANLink{Name: r.PostFormValue("name"), Notes: r.PostFormValue("notes")}
	for _, f := range []struct {
		name string
		dst  *int64
	}{{"down_mbps", &l.DownMbps}, {"up_mbps", &l.UpMbps}} {
		if v := strings.TrimSpace(r.PostFormValue(f.name)); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, false
			}
			*f.dst = n
		}
	}
	if v := strings.TrimSpace(r.PostFormValue("sort")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, false
		}
		l.Sort = n
	}
	return l, true
}

// wanErrKey maps a store error to the flash key of the WAN page.
func wanErrKey(err error) (string, bool) {
	switch {
	case errors.Is(err, store.ErrWANLinkInvalid):
		return "wan_invalid", true
	case errors.Is(err, store.ErrWANLinkExists):
		return "wan_exists", true
	case errors.Is(err, store.ErrWANLinkInUse):
		return "wan_in_use", true
	}
	return "", false
}

func (s *Server) wanSave(w http.ResponseWriter, r *http.Request, create bool) {
	l, ok := wanFromForm(r)
	if !ok {
		redirect(w, r, "/admin/wan?err=wan_invalid")
		return
	}
	var err error
	action := "wan.create"
	if create {
		err = s.DB.CreateWANLink(r.Context(), l)
	} else {
		id, ok := pathID(r)
		if !ok {
			s.errorPage(w, r, http.StatusNotFound, "error.not_found")
			return
		}
		l.ID, action = id, "wan.update"
		err = s.DB.UpdateWANLink(r.Context(), l)
	}
	if key, known := wanErrKey(err); known {
		redirect(w, r, "/admin/wan?err="+key)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, action, l.Name, "")
	if create {
		redirect(w, r, "/admin/wan?ok=wan_created")
		return
	}
	redirect(w, r, "/admin/wan?ok=saved")
}

func (s *Server) wanCreate(w http.ResponseWriter, r *http.Request) { s.wanSave(w, r, true) }
func (s *Server) wanUpdate(w http.ResponseWriter, r *http.Request) { s.wanSave(w, r, false) }

func (s *Server) wanDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "error.not_found")
		return
	}
	l, err := s.DB.GetWANLink(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.DB.DeleteWANLink(r.Context(), id); err != nil {
		if key, known := wanErrKey(err); known {
			redirect(w, r, "/admin/wan?err="+key)
			return
		}
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "wan.delete", l.Name, "")
	redirect(w, r, "/admin/wan?ok=wan_deleted")
}
