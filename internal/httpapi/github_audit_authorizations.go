package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

// Grants authorize AI usage, independently of ordinary project read access.
// Never accept an account owner from an API payload: it is the granting human.
func (s *Server) githubAuditAuthorizations(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference, id string) {
	if !requireAuditReadScope(w, identity) {
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
	if r.Method == http.MethodGet && id == "" {
		if !identity.IsToken && !requireAdmin(w, identity) {
			return
		}
		grants, err := s.Store.ListGithubAuditAuthorizations(r.Context(), project.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		visible := []store.GithubAuditAuthorization{}
		for _, grant := range grants {
			if !identity.IsToken || (grant.AgentID == identity.Actor.ID && grant.TokenID == identity.Token.Token.ID) {
				visible = append(visible, grant)
			}
		}
		s.writeJSON(w, 200, map[string]any{"data": visible})
		return
	}
	if !requireAdmin(w, identity) {
		return
	}
	if r.Method == http.MethodPost && id == "" {
		var input store.CreateGithubAuditAuthorizationInput
		fields, err := decodeJSONObject(r, &input)
		if err != nil {
			s.writeError(w, 400, "invalid_request", "invalid audit authorization", nil)
			return
		}
		for field, raw := range fields {
			if (field != "agent_id" && field != "token_id" && field != "expires_at" && field != "max_tasks") || isJSONNull(raw) {
				s.writeError(w, 400, "invalid_request", "unknown or null audit authorization field", nil)
				return
			}
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			grant, err := s.Store.CreateGithubAuditAuthorization(r.Context(), project.ID, identity.Actor.ID, input)
			if err != nil {
				return 0, nil, "", err
			}
			body, err := json.Marshal(grant)
			return 201, body, "", err
		})
		return
	}
	if r.Method == http.MethodDelete && id != "" {
		grant, err := s.Store.GetGithubAuditAuthorization(r.Context(), id)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if grant.ProjectID != project.ID {
			s.writeError(w, 404, "not_found", "audit authorization not found", nil)
			return
		}
		// Revalidate on every revoke; durable fencing precedes transport cancel.
		s.mutationRateOnly(w, r, identity, func() (int, []byte, string, error) {
			grant, err := s.Store.RevokeGithubAuditAuthorization(r.Context(), id, identity.Actor.ID)
			if err != nil {
				return 0, nil, "", err
			}
			if s.GithubAudits != nil {
				s.GithubAudits.cancelAuthorization(r.Context(), id)
			}
			body, err := json.Marshal(grant)
			return 200, body, "", err
		})
		return
	}
	s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
}

func (s *Server) githubAuditAccountOwner(ctx context.Context, identity auth.Identity, projectID, authorizationID string) (string, error) {
	if !identity.IsToken && identity.Actor.Kind == "human" && identity.Actor.DisabledAt == nil {
		if authorizationID != "" {
			return "", &store.Error{Kind: store.ErrInvalid, Message: "human audits do not use agent authorizations"}
		}
		return identity.Actor.ID, nil
	}
	if !identity.IsToken || identity.Actor.Kind != "agent" || !identity.HasScope("tasks:read") || !identity.CanProject(projectID) || authorizationID == "" {
		return "", &store.Error{Kind: store.ErrForbidden, Message: "a matching unexpired agent audit authorization is required"}
	}
	grant, err := s.Store.ValidateGithubAuditAuthorization(ctx, authorizationID, projectID, identity.Actor.ID, identity.Token.Token.ID)
	if err != nil {
		return "", &store.Error{Kind: store.ErrForbidden, Message: "a matching unexpired agent audit authorization is required"}
	}
	return grant.AccountOwnerID, nil
}

func (g *GithubAuditService) authorizedJob(ctx context.Context, job store.GithubAuditJob) error {
	if job.AuthorizationID != "" {
		grant, err := g.store.ValidateGithubAuditAuthorization(ctx, job.AuthorizationID, job.ProjectID, job.ActorID, job.RequestTokenID)
		if err != nil {
			return err
		}
		if grant.AccountOwnerID != job.AccountOwnerID || grant.SelectionVersion != job.SelectionVersion || job.TaskCount > grant.MaxTasks {
			return store.ErrForbidden
		}
	} else if job.AccountOwnerID != job.ActorID {
		return store.ErrForbidden
	}
	return g.authorized(ctx, job.AccountOwnerID, job.ProjectID, job.SelectionVersion, job.ProviderGeneration, job.AccountGeneration)
}

func (g *GithubAuditService) cancelAuthorization(ctx context.Context, authorizationID string) {
	g.mu.Lock()
	ids := make([]string, 0, len(g.active))
	for id := range g.active {
		ids = append(ids, id)
	}
	g.mu.Unlock()
	for _, id := range ids {
		job, err := g.store.GetGithubAuditJob(ctx, id)
		if err == nil && job.AuthorizationID == authorizationID {
			g.mu.Lock()
			cancel := g.active[id]
			g.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
	}
}
