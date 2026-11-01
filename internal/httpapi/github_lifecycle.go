package httpapi

import (
	"context"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

// configureGithubServices performs no network calls and starts no goroutines.
// The executable owns service lifetime; tests may inject their own provider.
func (s *Server) configureGithubServices() {
	provider := githubprovider.New(githubprovider.Options{
		Enabled: s.Cfg.GitHubEnabled, TokenFile: s.Cfg.GitHubTokenFile,
		Repositories: s.Cfg.GitHubRepositories,
	})
	s.Github = NewGithubService(s.Store, provider)
	runner, _ := s.Codex.(GithubEvidenceRunner)
	s.GithubAudits = NewGithubAuditService(s.Store, provider, runner, s.githubAuditPolicy)
}

func (s *Server) githubAuditPolicy(ctx context.Context, actorID, projectID string) error {
	if !s.Cfg.GitHubEnabled || s.Cfg.LunaDisabled {
		return &store.Error{Kind: store.ErrForbidden, Message: "GitHub audits are disabled on this Helm instance"}
	}
	actor, err := s.Store.GetActor(ctx, actorID)
	if err != nil {
		return err
	}
	identity := auth.Identity{Actor: actor}
	if actor.Kind != "human" || actor.DisabledAt != nil || !identity.CanProject(projectID) {
		return &store.Error{Kind: store.ErrForbidden, Message: "This account cannot audit the selected project"}
	}
	return nil
}

// StartGithubServices keeps the browser request lifetime out of background work.
func (s *Server) StartGithubServices(ctx context.Context) {
	if !s.Cfg.GitHubEnabled {
		return
	}
	if s.Github != nil {
		s.Github.Start(ctx)
	}
	if s.GithubAudits != nil {
		s.GithubAudits.Start(ctx)
	}
}

func (s *Server) StopGithubServices(ctx context.Context) error {
	var err error
	if s.GithubAudits != nil {
		err = s.GithubAudits.Close(ctx)
	}
	if s.Github != nil {
		s.Github.Close()
	}
	return err
}
