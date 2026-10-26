package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

// exportPortable serves the versioned portability snapshot. The endpoint is
// intentionally separate from database backup/restore: it only returns live,
// user-facing records and never exposes credentials or deployment internals.
func (s *Server) exportPortable(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string, projectRoute bool) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireScope(w, identity, "projects:read") || !requireScope(w, identity, "tasks:read") || !requireScope(w, identity, "events:read") {
		return
	}
	version := store.PortableVersion
	if values, present := r.URL.Query()["version"]; present {
		if len(values) != 1 || (values[0] != "2" && values[0] != "3") {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "portable export version must be 2 or 3", nil)
			return
		}
		if values[0] == "3" {
			version = store.PortableVersionGithub
		}
	}
	var projectIDs []string
	if projectRoute {
		project, err := s.Store.GetProject(r.Context(), reference)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if !identity.CanProject(project.ID) {
			s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
			return
		}
		projectIDs = []string{project.ID}
	} else {
		projectReference, err := parseOptionalIdentifier(r, "project")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		if projectReference != "" {
			project, err := s.Store.GetProject(r.Context(), projectReference)
			if err != nil {
				s.writeStoreError(w, err)
				return
			}
			if !identity.CanProject(project.ID) {
				s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
				return
			}
			projectIDs = []string{project.ID}
		} else {
			projectIDs = scopedProjectIDs(identity)
		}
	}
	archive, err := s.Store.ExportPortableVersion(r.Context(), projectIDs, version)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !s.portableGithubSourceRead(w, r, archive) {
		return
	}
	body, err := json.Marshal(archive)
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="helm-portable-v%d.json"`, archive.Version))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Helm-Portable-Format", store.PortableFormat)
	w.Header().Set("X-Helm-Portable-Version", fmt.Sprintf("%d", archive.Version))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// portableGithubSourceRead authorizes every retained source manifest before any
// archive bytes are written. A run with no findings can still retain private
// source text, so evidence checks are independent of finding access checks.
func (s *Server) portableGithubSourceRead(w http.ResponseWriter, r *http.Request, archive store.PortableArchive) bool {
	if archive.Github == nil {
		return true
	}
	projects := make(map[string]string, len(archive.Github.AuditRuns))
	for _, run := range archive.Github.AuditRuns {
		projects[run.ID] = run.ProjectID
	}
	unavailable := func() bool {
		s.writeError(w, http.StatusConflict, "evidence_unavailable", "Source access must be restored before exporting audit evidence", nil)
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	for _, evidence := range archive.Github.AuditEvidence {
		projectID := projects[evidence.AuditID]
		if projectID == "" || s.GithubAudits == nil {
			return unavailable()
		}
		if err := s.GithubAudits.evidenceAccess(ctx, projectID, evidence); err != nil {
			return unavailable()
		}
	}
	checked := make(map[string]bool)
	for _, finding := range archive.Github.AuditFindings {
		if checked[finding.AuditID] {
			continue
		}
		projectID := projects[finding.AuditID]
		if projectID == "" {
			return unavailable()
		}
		if !s.githubAuditSourceRead(w, r.WithContext(ctx), finding.AuditID, projectID) {
			return false
		}
		checked[finding.AuditID] = true
	}
	return true
}
