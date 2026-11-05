package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/KanterLabs/helm/internal/attachments"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request, identity auth.Identity, task store.Task) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 255 {
		s.writeError(w, 400, "invalid_idempotency_key", "a stable Idempotency-Key of at most 255 bytes is required", nil)
		return
	}
	if !s.admitMutationRate(w, r, identity) {
		return
	}
	limits := s.attachmentLimits()
	r.Body = http.MaxBytesReader(w, r.Body, limits.MaxFileBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		s.writeError(w, 400, "invalid_request", "multipart/form-data with one file is required", nil)
		return
	}
	// The shared lease prevents cleanup from touching a live staging upload.
	unlock, err := attachments.LockGC(s.attachmentRoot(), false)
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	defer unlock()
	var prepared *attachments.Prepared
	defer func() {
		if prepared != nil {
			prepared.Cleanup()
		}
	}()
	a := store.Attachment{TaskID: task.ID, ProjectID: task.ProjectID, ActorID: identity.Actor.ID}
	fields := map[string]bool{}
	metadataBytes := 0
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			s.attachmentUploadError(w, e)
			return
		}
		name := part.FormName()
		if name == "file" {
			if prepared != nil || part.FileName() == "" {
				s.writeError(w, 400, "invalid_request", "exactly one image file is required", nil)
				return
			}
			prepared, err = attachments.Prepare(s.attachmentRoot(), part, part.FileName(), limits)
			if err != nil {
				s.attachmentUploadError(w, err)
				return
			}
			continue
		}
		if fields[name] || len(fields) > 8 {
			s.writeError(w, 400, "invalid_request", "invalid or repeated metadata field", nil)
			return
		}
		fields[name] = true
		data, e := io.ReadAll(io.LimitReader(part, 4097))
		if e != nil || len(data) > 4096 {
			s.writeError(w, 400, "invalid_request", "attachment metadata is too large", nil)
			return
		}
		metadataBytes += len(data)
		if metadataBytes > 8192 {
			s.writeError(w, 400, "invalid_request", "attachment metadata is too large", nil)
			return
		}
		value := string(data)
		switch name {
		case "caption":
			a.Caption = value
		case "commit_sha":
			a.CommitSHA = value
		case "build":
			a.Build = value
		case "viewport_width", "viewport_height":
			n, e := strconv.Atoi(value)
			if e != nil {
				s.writeError(w, 400, "invalid_request", "viewport dimensions must be integers", nil)
				return
			}
			if name == "viewport_width" {
				a.ViewportWidth = n
			} else {
				a.ViewportHeight = n
			}
		default:
			s.writeError(w, 400, "invalid_request", "unknown attachment metadata field", nil)
			return
		}
	}
	if prepared == nil {
		s.writeError(w, 400, "invalid_request", "an image file is required", nil)
		return
	}
	if err = validateAttachmentMetadata(a); err != nil {
		s.writeError(w, 400, "invalid_request", err.Error(), nil)
		return
	}
	a.Filename = prepared.Filename
	a.ContentType = prepared.ContentType
	a.SizeBytes = prepared.SizeBytes
	a.StoredBytes = prepared.StoredBytes
	a.SHA256 = prepared.SHA256
	a.Width = prepared.Width
	a.Height = prepared.Height
	encoded, _ := json.Marshal(a)
	sum := sha256.Sum256(encoded)
	hash := hex.EncodeToString(sum[:])
	// Store reservation and keyed replay remain serialized through installation;
	// no body bytes are kept by the global JSON idempotency response cache.
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	a, replay, err := s.Store.ReserveAttachment(r.Context(), a, key, hash, limits.ProjectQuotaBytes)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if replay {
		checked, e := s.Store.GetAttachment(r.Context(), a.ID)
		if e != nil {
			s.writeStoreError(w, e)
			return
		}
		path, e := attachments.ObjectPath(s.attachmentRoot(), a.ID, false)
		if e != nil {
			s.writeError(w, 404, "not_found", "attachment content is unavailable", nil)
			return
		}
		info, e := os.Stat(path)
		if e != nil || !info.Mode().IsRegular() || info.Size() != a.SizeBytes {
			_ = s.Store.SetAttachmentReadiness(r.Context(), a.ID, false)
			s.writeError(w, 404, "not_found", "attachment content is unavailable", nil)
			return
		}
		a = checked
	}
	if !replay {
		if err = prepared.Install(s.attachmentRoot(), a.ID); err != nil {
			_ = s.Store.CancelAttachmentReservation(r.Context(), a.ID)
			s.writeInternal(w, err)
			return
		}
		if err = s.Store.MarkAttachmentReady(r.Context(), a.ID); err != nil {
			s.writeInternal(w, err)
			return
		}
	}
	w.Header().Set("ETag", attachmentETag(a))
	w.Header().Set("Location", "/api/v1/tasks/"+task.ID+"/attachments/"+a.ID)
	status := 201
	if replay {
		status = 200
	}
	s.writeJSON(w, status, a)
}
func (s *Server) attachmentUploadError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) || errors.Is(err, attachments.ErrTooLarge) {
		s.writeError(w, 413, "request_too_large", "attachment upload exceeds the allowed size", nil)
		return
	}
	if errors.Is(err, attachments.ErrBusy) {
		w.Header().Set("Retry-After", "1")
		s.writeError(w, 503, "server_busy", err.Error(), nil)
		return
	}
	if errors.Is(err, attachments.ErrInvalid) {
		s.writeError(w, 400, "invalid_image", err.Error(), nil)
		return
	}
	s.writeError(w, 400, "invalid_upload", "could not process image upload", nil)
}
