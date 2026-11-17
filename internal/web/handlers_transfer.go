package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/store"
	"github.com/lan-dot-party/lanleecher/internal/transfer"
	"github.com/lan-dot-party/lanleecher/pkg/version"
)

// uploadTTL is how long an uploaded file waits for the confirmation.
const uploadTTL = 15 * time.Minute

var uploadID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// transferView is the data of the transfer page.
type transferView struct {
	Events []*store.Event
	// Report is set for the preview and for the final result.
	Report *store.ImportReport
	Doc    *transfer.Doc
	// ExportedAt is Doc's export time.
	ExportedAt time.Time
	// Done is true when Report is the result of a real import.
	Done     bool
	Backup   string
	ID       string
	Conflict string
}

func (s *Server) importDir() string {
	return filepath.Join(filepath.Dir(s.Cfg.ResolvePath(s.Cfg.Storage.Path)), "import")
}

func (s *Server) backupDir() string {
	return filepath.Join(filepath.Dir(s.Cfg.ResolvePath(s.Cfg.Storage.Path)), "backups")
}

// cleanUploads removes uploads that were never confirmed.
func (s *Server) cleanUploads() {
	entries, err := os.ReadDir(s.importDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > uploadTTL {
			_ = os.Remove(filepath.Join(s.importDir(), e.Name()))
		}
	}
}

func (s *Server) transferRender(w http.ResponseWriter, r *http.Request, status int, v *transferView, errKey, errText string) {
	if v.Events == nil {
		events, err := s.DB.ListEvents(r.Context(), "", time.Time{}, time.Time{})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v.Events = events
	}
	if v.Doc != nil {
		v.ExportedAt = time.Unix(v.Doc.ExportedAt, 0)
	}
	p := s.page(r, translate(language(r), "admin.transfer"), "admin", v)
	if errKey != "" {
		p.Error = p.T(errKey)
	}
	if errText != "" {
		p.Error = errText
	}
	s.renderStatus(w, r, status, "transfer", "layout", p)
}

func (s *Server) transferPage(w http.ResponseWriter, r *http.Request) {
	s.cleanUploads()
	s.transferRender(w, r, http.StatusOK, &transferView{}, "", "")
}

func (s *Server) transferExport(w http.ResponseWriter, r *http.Request) {
	doc, _, err := s.DB.Export(r.Context(), store.ExportOptions{Events: r.URL.Query()["event"],
		Software: "lanleecher " + version.GetVersion(), Source: s.Cfg.PublicURL})
	if errors.Is(err, store.ErrNotFound) {
		redirect(w, r, "/admin/transfer?err=transfer_unknown_event")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Audit(r, "transfer.export", "", fmt.Sprintf("%d events, %d MACs", len(doc.Events), len(doc.MACs)))
	h := w.Header()
	h.Set("Content-Type", "application/gzip")
	h.Set("Content-Disposition", `attachment; filename="lanleecher-export-`+time.Now().Format("2006-01-02")+`.json.gz"`)
	h.Set("Cache-Control", "no-store")
	if err := transfer.Write(w, doc); err != nil {
		s.Log.Error("write export", "err", err)
	}
}

func newUploadID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func conflictValue(r *http.Request) string {
	if r.PostFormValue("event_conflict") == store.ConflictSkip {
		return store.ConflictSkip
	}
	return store.ConflictNew
}

// readUpload opens and parses a stored upload.
func (s *Server) readUpload(id string) (*transfer.Doc, error) {
	f, err := os.Open(filepath.Join(s.importDir(), id+".json.gz"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return transfer.Read(f)
}

func (s *Server) removeUpload(id string) {
	_ = os.Remove(filepath.Join(s.importDir(), id+".json.gz"))
}

func (s *Server) transferImport(w http.ResponseWriter, r *http.Request) {
	// The csrf middleware already parsed the multipart body with a size limit.
	// A bad request here would have been rejected there.
	file, _, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.transferRender(w, r, http.StatusRequestEntityTooLarge, &transferView{}, "error.transfer_too_large", "")
			return
		}
		s.transferRender(w, r, http.StatusBadRequest, &transferView{}, "error.transfer_no_file", "")
		return
	}
	defer file.Close()
	conflict := conflictValue(r)

	if err := os.MkdirAll(s.importDir(), 0o700); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := newUploadID()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	path := filepath.Join(s.importDir(), id+".json.gz")
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	n, err := io.Copy(out, io.LimitReader(file, transfer.MaxCompressed+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		s.removeUpload(id)
		s.fail(w, r, err)
		return
	}
	if n > transfer.MaxCompressed {
		s.removeUpload(id)
		s.transferRender(w, r, http.StatusRequestEntityTooLarge, &transferView{}, "error.transfer_too_large", "")
		return
	}
	doc, err := s.readUpload(id)
	if err != nil {
		s.removeUpload(id)
		s.transferReadError(w, r, err)
		return
	}
	rep, err := s.DB.Import(r.Context(), doc, store.ImportOptions{EventConflict: conflict, DryRun: true})
	if err != nil {
		s.removeUpload(id)
		s.fail(w, r, err)
		return
	}
	s.transferRender(w, r, http.StatusOK, &transferView{Report: rep, Doc: doc, ID: id, Conflict: conflict}, "", "")
}

// transferReadError shows why a file could not be read. Validation messages
// are safe to show; the template escapes them.
func (s *Server) transferReadError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *transfer.ValidationError
	switch {
	case errors.Is(err, transfer.ErrTooLarge):
		s.transferRender(w, r, http.StatusRequestEntityTooLarge, &transferView{}, "error.transfer_too_large", "")
	case errors.As(err, &ve):
		s.transferRender(w, r, http.StatusBadRequest, &transferView{}, "", ve.Error())
	default:
		s.transferRender(w, r, http.StatusBadRequest, &transferView{}, "error.transfer_invalid", "")
	}
}

// uploadFromForm validates the id of a confirm or cancel request.
func (s *Server) uploadFromForm(r *http.Request) (string, bool) {
	id := r.PostFormValue("id")
	return id, uploadID.MatchString(id)
}

func (s *Server) transferConfirm(w http.ResponseWriter, r *http.Request) {
	id, ok := s.uploadFromForm(r)
	if !ok {
		s.errorPage(w, r, http.StatusBadRequest, "error.bad_request")
		return
	}
	info, err := os.Stat(filepath.Join(s.importDir(), id+".json.gz"))
	if err != nil || time.Since(info.ModTime()) > uploadTTL {
		s.removeUpload(id)
		redirect(w, r, "/admin/transfer?err=transfer_expired")
		return
	}
	doc, err := s.readUpload(id)
	if err != nil {
		s.removeUpload(id)
		s.transferReadError(w, r, err)
		return
	}
	backup := filepath.Join(s.backupDir(), "lanleecher-before-import-"+time.Now().Format("2006-01-02-150405")+".db")
	if err := s.DB.Backup(r.Context(), backup); err != nil {
		s.Log.Error("backup before import", "err", err)
		s.transferRender(w, r, http.StatusInternalServerError, &transferView{}, "error.transfer_backup", "")
		return
	}
	rep, err := s.DB.Import(r.Context(), doc, store.ImportOptions{EventConflict: conflictValue(r)})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.removeUpload(id)
	s.Audit(r, "transfer.import", id, importSummary(rep))
	if err := s.Sched.Reload(r.Context()); err != nil {
		s.Log.Error("reload pollers", "err", err)
	}
	s.transferRender(w, r, http.StatusOK, &transferView{Report: rep, Doc: doc, Done: true, Backup: filepath.Base(backup)}, "", "")
}

func (s *Server) transferCancel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.uploadFromForm(r)
	if !ok {
		s.errorPage(w, r, http.StatusBadRequest, "error.bad_request")
		return
	}
	s.removeUpload(id)
	redirect(w, r, "/admin/transfer?ok=transfer_cancelled")
}

// importSummary is the audit detail of an import.
func importSummary(rep *store.ImportReport) string {
	imported := 0
	for _, ev := range rep.Events {
		if ev.Action != store.ActionSkipped {
			imported++
		}
	}
	return fmt.Sprintf("%d switches, %d new MACs, %d merged MACs, %d events", len(rep.Switches), rep.MACsNew,
		rep.MACsMerged, imported)
}
