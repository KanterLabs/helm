package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

func (s *Server) githubService() *GithubService {
	if s.Github != nil {
		return s.Github
	}
	return NewGithubService(s.Store, nil)
}
func (s *Server) githubIntegration(w http.ResponseWriter, r *http.Request, identity auth.Identity, check bool) {
	if !requireAdmin(w, identity) {
		return
	}
	if (check && r.Method != http.MethodPost) || (!check && r.Method != http.MethodGet) {
		s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	diagnostics := s.githubService().Diagnostics(ctx, check)
	body, err := json.Marshal(diagnostics)
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	s.writeRaw(w, 200, body, "")
}
func (s *Server) projectGithub(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if !requireScope(w, identity, "tasks:read") {
		return
	}
	project, err := s.Store.GetProject(r.Context(), reference)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(project.ID) {
		s.writeError(w, 403, "forbidden", "token is not scoped to this project", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		selection, err := s.Store.GetGithubSelection(r.Context(), project.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		body, _ := json.Marshal(selection)
		s.writeRaw(w, 200, body, projectETag(selection.Version))
	case http.MethodPut:
		if !requireAdmin(w, identity) {
			return
		}
		version, err := parseVersion(r)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		var input struct {
			Repositories []store.GithubRepository `json:"repositories"`
		}
		fields, err := decodeJSONObject(r, &input)
		if err != nil || len(fields) != 1 || fields["repositories"] == nil || isJSONNull(fields["repositories"]) {
			s.writeError(w, 400, "invalid_request", "repositories must be an array", nil)
			return
		}
		for i := range input.Repositories {
			repository, err := githubprovider.NormalizeRepository(input.Repositories[i].Repository)
			if err != nil || !s.githubService().repositoryAllowed(repository) {
				s.writeError(w, 400, "invalid_request", "repository is outside the configured GitHub allowlist", nil)
				return
			}
			input.Repositories[i].Repository = repository
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			selection, err := s.Store.UpdateGithubSelection(r.Context(), project.ID, input.Repositories, version)
			if err != nil {
				return 0, nil, "", err
			}
			body, err := json.Marshal(selection)
			return 200, body, projectETag(selection.Version), err
		})
	default:
		s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
	}
}
func (s *Server) projectGithubReadiness(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if r.Method != http.MethodGet {
		s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	if identity.IsToken || identity.Actor.Kind != "human" {
		s.writeError(w, 403, "human_account_required", "a signed-in human account is required", nil)
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
	selection, err := s.Store.GetGithubSelection(r.Context(), project.ID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	diagnostics := s.githubService().Diagnostics(ctx, true)
	github := map[string]string{"state": diagnostics.State, "message": diagnostics.Message}
	ai := map[string]string{"state": "not_connected", "message": "Connect your personal AI account."}
	if s.Cfg.LunaDisabled {
		ai = map[string]string{"state": "disabled", "message": "AI assistance is disabled by the operator."}
	} else if s.Codex == nil {
		ai = map[string]string{"state": "unavailable", "message": "AI assistance is unavailable."}
	} else {
		account, accountErr := s.Codex.Account(ctx, identity.Actor.ID, false)
		if accountErr != nil {
			ai = map[string]string{"state": "unavailable", "message": "AI account status is unavailable."}
		} else if account.Connected && account.AccountType == "chatgpt" {
			ai = map[string]string{"state": "connected", "message": "Your AI account is connected."}
		}
	}
	ready := diagnostics.State == "connected" && ai["state"] == "connected" && len(selection.Repositories) > 0
	if _, err := s.githubService().SelectionAllowed(r.Context(), project.ID); err != nil {
		ready = false
	}
	body, _ := json.Marshal(map[string]any{"ready": ready, "github": github, "ai": ai, "repositories": selection.Repositories})
	s.writeRaw(w, 200, body, "")
}
func (s *Server) taskArtifacts(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference, artifactID string, refresh bool) {
	scope := "tasks:read"
	if r.Method == http.MethodDelete || (r.Method == http.MethodPost && !refresh) {
		scope = "tasks:write"
	}
	if !requireScope(w, identity, scope) {
		return
	}
	// Link responses include cached private metadata, including on replay.
	// Authorize that read before resolving or mutating the task.
	if r.Method == http.MethodPost && !refresh && artifactID == "" && !requireScope(w, identity, "tasks:read") {
		return
	}
	task, err := s.Store.ResolveTaskReference(r.Context(), reference)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(task.ProjectID) {
		s.writeError(w, 403, "forbidden", "token is not scoped to this project", nil)
		return
	}
	service := s.githubService()
	if refresh {
		if r.Method != http.MethodPost || artifactID == "" {
			s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
			return
		}
		artifact, err := s.Store.GetGithubArtifact(r.Context(), task.ID, artifactID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		artifact, err = service.Refresh(r.Context(), artifact)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		body, _ := json.Marshal(artifact)
		s.writeRaw(w, 200, body, taskETag(task))
		return
	}
	if r.Method == http.MethodGet && artifactID == "" {
		artifacts, err := service.Artifacts(r.Context(), task.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		body, _ := json.Marshal(map[string]any{"data": artifacts, "next_cursor": "", "task_version": task.Version})
		s.writeRaw(w, 200, body, taskETag(task))
		return
	}
	if (r.Method != http.MethodPost || artifactID != "") && (r.Method != http.MethodDelete || artifactID == "") {
		s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	version, err := parseVersion(r)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			updated, err := s.Store.DetachGithubArtifact(r.Context(), task.ID, artifactID, version, identity.Actor.ID, !identity.IsToken && identity.Actor.Admin)
			if err != nil {
				return 0, nil, "", err
			}
			body, err := json.Marshal(map[string]any{"task_version": updated.Version})
			return 200, body, taskETag(updated), err
		})
		return
	}
	var input struct {
		URL string `json:"url"`
	}
	fields, err := decodeJSONObject(r, &input)
	if err != nil || len(fields) != 1 || fields["url"] == nil {
		s.writeError(w, 400, "invalid_request", "a GitHub pull request URL is required", nil)
		return
	}
	parsed, err := githubprovider.ParsePullRequestURL(input.URL)
	if err != nil || !service.repositoryAllowed(parsed.Repository) {
		s.writeError(w, 400, "invalid_request", "use an allowed GitHub pull request URL", nil)
		return
	}
	s.mutation(&githubArtifactResponse{ResponseWriter: w, ctx: r.Context(), service: service, taskID: task.ID}, r, identity, func() (int, []byte, string, error) {
		artifact, updated, err := s.Store.LinkGithubArtifact(r.Context(), task.ID, parsed.Repository, parsed.Number, parsed.URL, version, identity.Actor.ID, !identity.IsToken && identity.Actor.Admin)
		if err != nil {
			return 0, nil, "", err
		}
		// A valid allowed explicit link survives provider failures. Refresh records
		// a safe error code; permission loss hides cache for every linked task.
		refreshed, refreshErr := service.Refresh(r.Context(), artifact)
		if refreshErr == nil {
			artifact = refreshed
		} else {
			artifact = service.MaskArtifact(artifact)
		}
		artifact.Snapshot = nil // Hydrated after replay lookup; never retained in idempotency.
		body, err := json.Marshal(map[string]any{"artifact": artifact, "task_version": updated.Version})
		return http.StatusCreated, body, taskETag(updated), err
	})
}
func (s *Server) artifactSummaries(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if r.Method != http.MethodGet {
		s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireScope(w, identity, "tasks:read") {
		return
	}
	values, ok := r.URL.Query()["task_ids"]
	if !ok || len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		s.writeError(w, 400, "invalid_request", "task_ids must contain one to 200 immutable task IDs", nil)
		return
	}
	ids := strings.Split(values[0], ",")
	if len(ids) > 200 {
		s.writeError(w, 400, "invalid_request", "at most 200 task IDs may be queried", nil)
		return
	}
	seen := map[string]bool{}
	unique := []string{}
	for _, id := range ids {
		if strings.TrimSpace(id) != id || id == "" || len(id) > 200 {
			s.writeError(w, 400, "invalid_request", "invalid immutable task ID", nil)
			return
		}
		if !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}
	projects, err := s.Store.GithubTaskProjects(r.Context(), unique)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	for _, id := range unique {
		if !identity.CanProject(projects[id]) {
			s.writeError(w, 403, "forbidden", "token is not scoped to every requested task", nil)
			return
		}
	}
	artifacts, err := s.Store.ListGithubArtifactsForTasks(r.Context(), unique)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	for i := range artifacts {
		artifacts[i] = s.githubService().MaskArtifact(artifacts[i])
	}
	body, _ := json.Marshal(map[string]any{"data": store.GithubSummaries(unique, artifacts)})
	s.writeRaw(w, 200, body, "")
}
