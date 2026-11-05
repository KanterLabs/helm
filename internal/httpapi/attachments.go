package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/KanterLabs/helm/internal/attachments"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func (s *Server) attachmentRoot() string { return filepath.Join(filepath.Dir(s.Cfg.DB), "attachments") }
func (s *Server) attachmentLimits() attachments.Limits {
	l := attachments.DefaultLimits()
	if s.Cfg.AttachmentMaxFileBytes > 0 {
		l.MaxFileBytes = s.Cfg.AttachmentMaxFileBytes
	}
	if s.Cfg.AttachmentProjectQuotaBytes > 0 {
		l.ProjectQuotaBytes = s.Cfg.AttachmentProjectQuotaBytes
	}
	return l
}
func isPortableBundleUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := splitPath(strings.TrimPrefix(r.URL.Path, "/api/v1"))
	return (len(p) == 2 && p[0] == "import" && p[1] == "bundle") || (r.URL.Query().Get("format") == "bundle" && ((len(p) == 1 && p[0] == "import") || (len(p) == 3 && p[0] == "projects" && p[2] == "import")))
}
func isAttachmentUpload(r *http.Request) bool {
	p := splitPath(strings.TrimPrefix(r.URL.Path, "/api/v1"))
	return r.Method == http.MethodPost && len(p) == 3 && p[0] == "tasks" && p[2] == "attachments"
}
func (s *Server) dispatchAttachments(w http.ResponseWriter, r *http.Request, identity auth.Identity, parts []string) bool {
	if len(parts) == 2 && parts[0] == "import" && parts[1] == "bundle" {
		s.importPortable(w, r, identity, "", false)
		return true
	}
	if len(parts) == 2 && parts[0] == "attachments" && parts[1] == "capabilities" {
		if !requireScope(w, identity, "tasks:read") {
			return true
		}
		if r.Method != http.MethodGet {
			s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
			return true
		}
		l := s.attachmentLimits()
		s.writeJSON(w, 200, map[string]any{"enabled": true, "types": []string{"image/png", "image/jpeg", "image/webp"}, "max_file_bytes": l.MaxFileBytes, "max_pixels": l.MaxPixels, "max_dimension": l.MaxDimension, "max_images_per_update": l.MaxImagesPerUpdate, "project_quota_bytes": l.ProjectQuotaBytes})
		return true
	}
	if len(parts) < 3 || len(parts) > 5 || parts[0] != "tasks" || parts[2] != "attachments" {
		return false
	}
	scope := "tasks:read"
	if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
		scope = "tasks:write"
	}
	if !requireScope(w, identity, scope) {
		return true
	}
	task, err := s.Store.ResolveTaskReference(r.Context(), parts[1])
	if err != nil {
		s.writeStoreError(w, err)
		return true
	}
	if !identity.CanProject(task.ProjectID) {
		s.writeError(w, 403, "forbidden", "access to this project is required", nil)
		return true
	}
	if len(parts) == 3 {
		switch r.Method {
		case http.MethodPost:
			s.uploadAttachment(w, r, identity, task)
		case http.MethodGet:
			limit, offset, err := parsePagination(r, 50)
			if err != nil {
				s.writeError(w, 400, "invalid_request", "invalid pagination", nil)
				return true
			}
			items, err := s.Store.ListAttachments(r.Context(), task.ID, identity.Actor.ID, r.URL.Query().Get("staged") == "true", identity.IsAdmin(), limit+1, offset)
			if err != nil {
				s.writeStoreError(w, err)
				return true
			}
			next := ""
			if len(items) > limit {
				items = items[:limit]
				next = encodeCursor(offset + limit)
			}
			s.writeCollection(w, items, next)
		default:
			s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
		}
		return true
	}
	a, err := s.Store.GetAttachment(r.Context(), parts[3])
	if err != nil {
		s.writeStoreError(w, err)
		return true
	}
	if a.TaskID != task.ID || a.ProjectID != task.ProjectID || (a.Status == "staged" && a.ActorID != identity.Actor.ID && !identity.IsAdmin()) {
		s.writeError(w, 404, "not_found", "attachment not found", nil)
		return true
	}
	if len(parts) == 5 {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
			return true
		}
		if parts[4] != "content" && parts[4] != "thumbnail" {
			s.writeError(w, 404, "not_found", "route not found", nil)
			return true
		}
		s.serveAttachmentContent(w, r, a, parts[4] == "thumbnail")
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		w.Header().Set("ETag", attachmentETag(a))
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
		} else {
			s.writeJSON(w, 200, a)
		}
	case http.MethodPatch, http.MethodDelete:
		s.mutateAttachment(w, r, identity, a)
	default:
		s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
	}
	return true
}
func attachmentETag(a store.Attachment) string { return `"v` + strconv.FormatInt(a.Version, 10) + `"` }
func (s *Server) serveAttachmentContent(w http.ResponseWriter, r *http.Request, a store.Attachment, thumb bool) {
	unlock, err := attachments.LockGC(s.attachmentRoot(), false)
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	defer unlock()
	path, err := attachments.ObjectPath(s.attachmentRoot(), a.ID, thumb)
	if err != nil {
		s.writeError(w, 404, "not_found", "attachment content is unavailable", nil)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.writeError(w, 404, "not_found", "attachment content is unavailable", nil)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		s.writeError(w, 404, "not_found", "attachment content is unavailable", nil)
		return
	}
	contentType := a.ContentType
	if thumb {
		contentType = "image/png"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"`+a.SHA256+fmt.Sprintf("-%t", thumb)+`"`)
	http.ServeContent(w, r, a.Filename, info.ModTime(), f)
}
func (s *Server) mutateAttachment(w http.ResponseWriter, r *http.Request, identity auth.Identity, a store.Attachment) {
	version, err := parseVersion(r)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !s.admitMutationRate(w, r, identity) {
		return
	}
	if r.Method == http.MethodPatch {
		var input struct {
			Caption        *string `json:"caption"`
			CommitSHA      *string `json:"commit_sha"`
			Build          *string `json:"build"`
			ViewportWidth  *int    `json:"viewport_width"`
			ViewportHeight *int    `json:"viewport_height"`
		}
		if _, err = decodeJSONObject(r, &input); err != nil {
			writeTaskInputError(w, err)
			return
		}
		if input.Caption != nil {
			a.Caption = *input.Caption
		}
		if input.CommitSHA != nil {
			a.CommitSHA = *input.CommitSHA
		}
		if input.Build != nil {
			a.Build = *input.Build
		}
		if input.ViewportWidth != nil {
			a.ViewportWidth = *input.ViewportWidth
		}
		if input.ViewportHeight != nil {
			a.ViewportHeight = *input.ViewportHeight
		}
	}
	if err = validateAttachmentMetadata(a); err != nil {
		s.writeError(w, 400, "invalid_request", err.Error(), nil)
		return
	}
	updated, err := s.Store.UpdateAttachment(r.Context(), a.ID, identity.Actor.ID, version, identity.IsAdmin(), r.Method == http.MethodDelete, a.Caption, a.CommitSHA, a.Build, a.ViewportWidth, a.ViewportHeight)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(204)
		return
	}
	w.Header().Set("ETag", attachmentETag(updated))
	s.writeJSON(w, 200, updated)
}
func validateAttachmentMetadata(a store.Attachment) error {
	if len(a.Caption) > 2000 || len(a.CommitSHA) > 128 || len(a.Build) > 200 || a.ViewportWidth < 0 || a.ViewportHeight < 0 || a.ViewportWidth > 100000 || a.ViewportHeight > 100000 || strings.ContainsAny(a.CommitSHA+a.Build, "\r\n\x00") {
		return errors.New("attachment metadata exceeds the allowed bounds")
	}
	return nil
}
