package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func (s *Server) githubAuditApply(w http.ResponseWriter, r *http.Request, identity auth.Identity, job store.GithubAuditJob) {
	var input struct {
		FindingIDs []string `json:"finding_ids"`
		Preview    *bool    `json:"preview"`
	}
	fields, err := decodeJSONObject(r, &input)
	if err != nil || len(fields) != 2 || len(input.FindingIDs) == 0 || len(input.FindingIDs) > 200 || input.Preview == nil {
		s.writeError(w, 400, "invalid_request", "finding_ids and explicit preview are required (maximum 200)", nil)
		return
	}
	seen := map[string]bool{}
	for _, id := range input.FindingIDs {
		if id == "" || seen[id] {
			s.writeError(w, 400, "invalid_request", "finding IDs must be unique", nil)
			return
		}
		seen[id] = true
		finding, err := s.Store.GetAuditFinding(r.Context(), id)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if finding.AuditID != job.AuditID {
			s.writeError(w, 400, "invalid_request", "finding is outside this audit", nil)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	evidence, err := s.Store.GetGithubAuditEvidence(ctx, job.AuditID)
	fresh := err == nil && !evidence.Partial && job.ProviderGeneration == s.GithubAudits.providerGeneration()
	if fresh {
		fresh = s.GithubAudits.evidenceAccess(ctx, job.ProjectID, evidence) == nil
	}
	if fresh {
		fresh = s.GithubAudits.fresh(ctx, evidence) == nil
	}
	if !fresh {
		results := []store.GithubAuditApplyResult{}
		for _, id := range input.FindingIDs {
			finding, _ := s.Store.GetAuditFinding(ctx, id)
			results = append(results, store.GithubAuditApplyResult{FindingID: id, TaskID: finding.TaskID, Outcome: "skipped", Message: "Source freshness or complete evidence could not be established"})
		}
		s.writeJSON(w, 200, map[string]any{"data": results})
		return
	}
	// Preview and Apply intentionally bypass cached HTTP idempotency responses:
	// every request rechecks remote freshness and current local state. The store's
	// deterministic finding receipt guarantees that Apply never moves twice.
	if !s.admitMutationRate(w, r, identity) {
		return
	}
	results := []store.GithubAuditApplyResult{}
	for _, id := range input.FindingIDs {
		if job.ProviderGeneration != s.GithubAudits.providerGeneration() {
			results = append(results, store.GithubAuditApplyResult{FindingID: id, Outcome: "skipped", Message: "GitHub connection changed; capture fresh evidence"})
			continue
		}
		result, err := s.Store.ApplyGithubAuditFinding(ctx, job, id, identity.Actor.ID, *input.Preview)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		results = append(results, result)
	}
	body, err := json.Marshal(map[string]any{"data": results})
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	s.writeRaw(w, 200, body, "")
}
