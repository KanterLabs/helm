package httpapi

import (
	"net/http"

	"github.com/KanterLabs/helm/internal/store"
)

// auditSourceRead fails closed for historical server GitHub audits and imported
// source manifests. Retiring the integration cannot authorize retained private
// reasons or cached review responses. Generic Board Audit remains available.
func (s *Server) auditSourceRead(w http.ResponseWriter, r *http.Request, auditID string) bool {
	protected, err := s.Store.GithubAuditProtected(r.Context(), auditID)
	if err != nil {
		s.writeStoreError(w, err)
		return false
	}
	if protected {
		s.writeRetiredEvidenceError(w)
		return false
	}
	return true
}

func (s *Server) writeRetiredEvidenceError(w http.ResponseWriter) {
	s.writeError(w, http.StatusConflict, "evidence_unavailable", "Historical GitHub audit evidence is unavailable because the integration was retired", nil)
}

// portableSourceRead runs before JSON or bundle bytes are returned. Even a
// cancelled capture with no findings can contain private source text. Human
// Board Audit findings also use the legacy archive section, so only protected
// findings are denied; ordinary audit history stays portable.
func (s *Server) portableSourceRead(w http.ResponseWriter, r *http.Request, archive store.PortableArchive) bool {
	if archive.Github == nil {
		return true
	}
	if len(archive.Github.AuditEvidence) > 0 {
		s.writeRetiredEvidenceError(w)
		return false
	}
	checked := make(map[string]bool)
	for _, finding := range archive.Github.AuditFindings {
		if checked[finding.AuditID] {
			continue
		}
		if !s.auditSourceRead(w, r, finding.AuditID) {
			return false
		}
		checked[finding.AuditID] = true
	}
	return true
}
