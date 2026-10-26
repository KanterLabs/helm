package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"net/http"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

type GithubAuditStartResponse struct {
	Audit store.AuditRun       `json:"audit"`
	Job   store.GithubAuditJob `json:"job"`
}

func (s *Server) githubAuditStart(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if r.Method != http.MethodPost {
		s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !s.githubAuditHuman(w, identity) {
		return
	}
	project, err := s.Store.GetProject(r.Context(), reference)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(project.ID) {
		s.writeError(w, 403, "forbidden", "project access required", nil)
		return
	}
	if s.idempotencyReplay(w, r, identity) {
		return
	}
	var input store.GithubAuditStartInput
	fields, err := decodeJSONObject(r, &input)
	if err != nil {
		s.writeError(w, 400, "invalid_json", "request body is invalid", nil)
		return
	}
	for field := range fields {
		if field != "repositories" && field != "states" && field != "task_ids" {
			s.writeError(w, 400, "invalid_request", "unknown audit scope field", nil)
			return
		}
		if isJSONNull(fields[field]) {
			s.writeError(w, 400, "invalid_request", "audit scope fields cannot be null", nil)
			return
		}
	}
	s.startGithubAudit(w, r, identity, project.ID, input, "")
}
func (s *Server) githubAuditHuman(w http.ResponseWriter, identity auth.Identity) bool {
	if identity.IsToken || identity.Actor.Kind != "human" || identity.Actor.DisabledAt != nil {
		s.writeError(w, 403, "human_account_required", "a signed-in human with a personal AI connection is required", nil)
		return false
	}
	if s.GithubAudits == nil {
		s.writeError(w, 503, "audit_unavailable", "server GitHub audits are unavailable", nil)
		return false
	}
	return true
}
func (s *Server) startGithubAudit(w http.ResponseWriter, r *http.Request, identity auth.Identity, projectID string, input store.GithubAuditStartInput, retryOf string) {
	if s.Cfg.LunaDisabled {
		s.writeError(w, 503, "ai_disabled", "AI assistance is disabled by the operator", nil)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		s.writeError(w, 400, "idempotency_key_required", "a bounded Idempotency-Key is required", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	selected, err := s.Store.GetGithubSelection(ctx, projectID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if input.Repositories == nil {
		input.Repositories = selected.Repositories
	}
	generation, err := s.GithubAudits.accountGeneration(ctx, identity.Actor.ID)
	if err != nil {
		s.writeError(w, 409, "ai_not_connected", "Connect your personal AI account before running an audit", nil)
		return
	}
	if err = s.GithubAudits.authorized(ctx, identity.Actor.ID, projectID, selected.Version, "", generation); err != nil {
		s.writeError(w, 409, "audit_not_ready", "Audit authorization or connection changed; check readiness", nil)
		return
	}
	diagnostics := s.GithubAudits.provider.Diagnostics(ctx)
	if diagnostics.State != "connected" {
		s.writeError(w, 409, "github_not_connected", diagnostics.Message, nil)
		return
	}
	fingerprint, _ := json.Marshal(struct {
		Scope   store.GithubAuditStartInput `json:"scope"`
		RetryOf string                      `json:"retry_of"`
	}{input, retryOf})
	sum := sha256.Sum256(fingerprint)
	input.RequestKey = key
	input.Fingerprint = hex.EncodeToString(sum[:])
	input.SelectionVersion = selected.Version
	input.ProviderGeneration = s.GithubAudits.providerGeneration()
	input.AccountGeneration = generation
	input.RetryOf = retryOf
	input.Model = "gpt-5.6-luna"
	input.Effort = "medium"
	s.mutationRateOnly(w, r, identity, func() (int, []byte, string, error) {
		run, job, err := s.Store.StartGithubAudit(ctx, projectID, identity.Actor.ID, input)
		if err != nil {
			return 0, nil, "", err
		}
		body, err := json.Marshal(GithubAuditStartResponse{Audit: run, Job: job})
		return 202, body, "", err
	})
}

// githubAuditJob dispatches the explicit job/cancel/retry/evidence/apply actions.
func (s *Server) githubAuditJob(w http.ResponseWriter, r *http.Request, identity auth.Identity, auditID, action string) {
	if !requireAuditReadScope(w, identity) {
		return
	}
	run, err := s.Store.GetAuditRun(r.Context(), auditID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(run.ProjectID) {
		s.writeError(w, 403, "forbidden", "project access required", nil)
		return
	}
	if action == "evidence" {
		if r.Method != http.MethodGet {
			s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
			return
		}
		if s.GithubAudits == nil {
			s.writeError(w, 503, "evidence_unavailable", "Evidence source access is unavailable", nil)
			return
		}
		evidence, err := s.Store.GetGithubAuditEvidence(r.Context(), auditID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err = s.GithubAudits.evidenceAccess(ctx, run.ProjectID, evidence); err != nil {
			s.writeError(w, 409, "evidence_unavailable", "Source access must be restored before viewing private evidence", nil)
			return
		}
		s.writeJSON(w, 200, evidence)
		return
	}
	job, err := s.Store.GetGithubAuditJob(r.Context(), auditID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if action == "job" {
		if r.Method != http.MethodGet {
			s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
			return
		}
		s.writeJSON(w, 200, job)
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !s.githubAuditHuman(w, identity) {
		return
	}
	if action == "cancel" {
		if job.ActorID != identity.Actor.ID && !identity.Actor.Admin {
			s.writeError(w, 403, "forbidden", "Only the originating human or project administrator may cancel", nil)
			return
		}
		s.mutationRateOnly(w, r, identity, func() (int, []byte, string, error) {
			j, err := s.GithubAudits.Cancel(r.Context(), auditID, "cancelled")
			body, _ := json.Marshal(j)
			return 200, body, "", err
		})
		return
	}
	if action == "retry" {
		if job.Outcome == "" {
			s.writeError(w, 409, "audit_active", "Cancel or finish this audit before retrying", nil)
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
		capture, err := s.Store.GetGithubAuditCapture(r.Context(), auditID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.startGithubAudit(w, r, identity, run.ProjectID, capture.Request, auditID)
		return
	}
	if action == "apply" {
		s.githubAuditApply(w, r, identity, job)
		return
	}
	s.writeError(w, 404, "not_found", "unknown audit action", nil)
}
func (g *GithubAuditService) evidenceAccess(ctx context.Context, projectID string, e store.GithubAuditEvidence) error {
	selection, err := g.store.GetGithubSelection(ctx, projectID)
	if err != nil {
		return err
	}
	if g.provider == nil || len(e.Repositories) == 0 {
		return store.ErrForbidden
	}
	generation := g.providerGeneration()
	knownAllowed := func(repo string) bool {
		if provider, ok := g.provider.(interface{ AccessAllowed(string) bool }); ok {
			return provider.AccessAllowed(repo)
		}
		return true
	}
	identities := map[string]int64{}
	for _, repo := range e.Repositories {
		allowed := false
		for _, selected := range selection.Repositories {
			if selected.Repository == repo.Repository && selected.Ref == repo.Ref {
				allowed = true
			}
		}
		if !allowed || !knownAllowed(repo.Repository) {
			return store.ErrForbidden
		}
		current, err := g.provider.Resolve(ctx, repo.Repository, repo.Ref)
		if err != nil {
			return err
		}
		if current.RepositoryID != repo.RepositoryID || !knownAllowed(repo.Repository) {
			return store.ErrForbidden
		}
		identities[repo.Repository] = repo.RepositoryID
	}
	capturedPRHeads := map[string]bool{}
	for _, item := range e.Items {
		if item.Kind != "pull_request" {
			continue
		}
		var captured githubprovider.PullRequestSnapshot
		if err := json.Unmarshal([]byte(item.Text), &captured); err != nil {
			return store.ErrForbidden
		}
		if captured.Repository != item.Repository || captured.HeadSHA != item.SHA || captured.RepositoryID != identities[item.Repository] || !knownAllowed(item.Repository) {
			return store.ErrForbidden
		}
		current, err := g.provider.FetchPullRequest(ctx, captured.Repository, captured.Number)
		if err != nil {
			return err
		}
		if current.RepositoryID != captured.RepositoryID || current.Partial || current.ChecksState == "unavailable" || !knownAllowed(item.Repository) {
			return store.ErrForbidden
		}
		capturedPRHeads[fmt.Sprintf("%s/%s", item.Repository, item.SHA)] = true
	}
	for _, item := range e.Items {
		if item.Kind == "check" && !capturedPRHeads[fmt.Sprintf("%s/%s", item.Repository, item.SHA)] {
			return store.ErrForbidden
		}
	}
	if generation != g.providerGeneration() {
		return store.ErrForbidden
	}
	return nil
}

// Source access is checked before legacy finding bodies or cached review
// responses can reveal reasons derived from a private repository.
func (s *Server) githubAuditSourceRead(w http.ResponseWriter, r *http.Request, auditID, projectID string) bool {
	protected, err := s.Store.GithubAuditProtected(r.Context(), auditID)
	if err != nil {
		s.writeStoreError(w, err)
		return false
	}
	if !protected {
		return true
	}
	run, err := s.Store.GetAuditRun(r.Context(), auditID)
	if err != nil {
		s.writeStoreError(w, err)
		return false
	}
	if run.FindingCount == 0 {
		return true
	}
	if s.GithubAudits == nil {
		s.writeError(w, 409, "evidence_unavailable", "Source access must be restored before reading audit findings", nil)
		return false
	}
	evidence, err := s.Store.GetGithubAuditEvidence(r.Context(), auditID)
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		err = s.GithubAudits.evidenceAccess(ctx, projectID, evidence)
	}
	if err != nil {
		s.writeError(w, 409, "evidence_unavailable", "Source access must be restored before reading audit findings", nil)
		return false
	}
	return true
}
