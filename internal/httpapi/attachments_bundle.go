package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

var portableBundleSlots = make(chan struct{}, 2)

// importPortableBundle is reached only after the common portable route has
// checked scopes and resolved/authorized the optional destination project.
func (s *Server) importPortableBundle(w http.ResponseWriter, r *http.Request, identity auth.Identity, targetProjectID string) {
	if scopedProjectIDs(identity) != nil && strings.TrimSpace(targetProjectID) == "" {
		s.writeError(w, 403, "forbidden", "project-scoped tokens must select an allowed target project", nil)
		return
	}
	dryRun, err := parseOptionalBool(r, "dry_run")
	if err != nil {
		s.writeError(w, 400, "invalid_request", err.Error(), nil)
		return
	}
	conflict, err := parseOptionalEnum(r, "conflict", map[string]struct{}{"remap": {}, "fail": {}})
	if err != nil {
		s.writeError(w, 400, "invalid_request", err.Error(), nil)
		return
	}
	if conflict == "" {
		conflict = "remap"
	}
	select {
	case portableBundleSlots <- struct{}{}:
		defer func() { <-portableBundleSlots }()
	default:
		s.writeError(w, 503, "busy", "portable image processing is busy; retry later", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, store.PortableBundleMaxBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, 413, "request_too_large", "bundle exceeds the upload limit or is unreadable", nil)
		return
	}
	// The generic JSON reader intentionally bypasses this route. Bind mutation
	// replay to ZIP bytes AND options, without retaining a second body copy.
	hash := sha256.New()
	hash.Write(data)
	hash.Write([]byte{0})
	hash.Write([]byte(r.URL.Query().Encode()))
	digest := hash.Sum(nil)
	r = r.WithContext(context.WithValue(r.Context(), requestBodyKey, digest))
	options := store.PortableImportOptions{TargetProjectID: targetProjectID, Conflict: conflict, DryRun: dryRun, ActorID: identity.Actor.ID}
	run := func() (int, []byte, string, error) {
		limits := s.attachmentLimits()
		report, e := s.Store.ImportPortableBundle(r.Context(), data, s.attachmentRoot(), options, limits.ProjectQuotaBytes, limits)
		if e != nil {
			return 0, nil, "", e
		}
		body, e := json.Marshal(report)
		return http.StatusOK, body, "", e
	}
	if dryRun && strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		status, body, _, e := run()
		if e != nil {
			s.writeStoreError(w, e)
			return
		}
		s.writeRaw(w, status, body, "")
		return
	}
	s.mutation(w, r, identity, run)
}

// portableTextScreenshotComments keeps legacy JSON importable when a comment
// contains only screenshots. The v5 archive retains the original empty body.
func (s *Server) portableTextScreenshotComments(r *http.Request, archive *store.PortableArchive) error {
	for index, comment := range archive.Comments {
		if strings.TrimSpace(comment.Body) != "" {
			continue
		}
		images, err := s.Store.AttachmentsForComment(r.Context(), comment.ID)
		if err != nil {
			return err
		}
		if len(images) > 0 {
			archive.Comments[index].Body = "[Screenshot omitted; export the version 5 bundle to preserve this update.]"
		}
	}
	return nil
}
