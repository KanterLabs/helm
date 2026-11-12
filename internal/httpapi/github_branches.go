package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/githubprovider"
)

type githubBranchesResponse struct {
	State        string                    `json:"state"`
	Message      string                    `json:"message"`
	Repository   string                    `json:"repository"`
	Repositories []string                  `json:"repositories"`
	Base         *githubprovider.BranchTip `json:"base"`
	Staging      *githubprovider.Branch    `json:"staging"`
	Branches     []githubprovider.Branch   `json:"branches"`
	Partial      bool                      `json:"partial"`
	FetchedAt    *time.Time                `json:"fetched_at"`
}

// Branches reads live branch state. Nothing is stored, so a revoked credential
// or repository loses access at the next request.
func (g *GithubService) Branches(ctx context.Context, repository string) (githubprovider.BranchReport, error) {
	if g == nil || g.provider == nil {
		return githubprovider.BranchReport{}, &githubprovider.Error{Code: "not_configured"}
	}
	return g.provider.Branches(ctx, repository)
}

// projectGithubBranches reports every branch of one repository the project
// selected. A provider failure is a state the view can explain, not an HTTP
// error, and no branch data accompanies a state other than "ready".
func (s *Server) projectGithubBranches(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if r.Method != http.MethodGet {
		s.writeError(w, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
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
	selection, err := s.Store.GetGithubSelection(r.Context(), project.ID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	response := githubBranchesResponse{State: "no_repository", Message: "Select a GitHub repository for this project in Settings.", Repositories: []string{}, Branches: []githubprovider.Branch{}}
	for _, selected := range selection.Repositories {
		if !slices.Contains(response.Repositories, selected.Repository) {
			response.Repositories = append(response.Repositories, selected.Repository)
		}
	}
	if requested := r.URL.Query().Get("repository"); requested != "" {
		normalized, err := githubprovider.NormalizeRepository(requested)
		if err != nil || !slices.Contains(response.Repositories, normalized) {
			s.writeError(w, 400, "invalid_request", "repository is not selected for this project", nil)
			return
		}
		response.Repository = normalized
	} else if len(response.Repositories) > 0 {
		response.Repository = response.Repositories[0]
	}
	if response.Repository != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		report, err := s.githubService().Branches(ctx, response.Repository)
		if err != nil {
			response.State, response.Message = githubprovider.ErrorCode(err), (&githubprovider.Error{Code: githubprovider.ErrorCode(err)}).Error()
		} else {
			response.State, response.Message = "ready", ""
			response.Base, response.Staging, response.Branches = &report.Base, report.Staging, report.Branches
			response.Partial, response.FetchedAt = report.Partial, &report.FetchedAt
		}
	}
	body, err := json.Marshal(response)
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	s.writeRaw(w, 200, body, "")
}
