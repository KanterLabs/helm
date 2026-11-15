package httpapi

import (
	"context"
	"log"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/githubconnect"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

// githubSeams carries the constructor-only test injection points of the
// connection manager (HTTP client, origins, clock) and of the provider (HTTP
// client, endpoint). Runtime configuration never sets them.
type githubSeams struct {
	connect  githubconnect.Options
	provider githubprovider.Options
}

// githubMode is operator_token when the operator names a token file, and
// device_flow otherwise (plan decision 3).
func (s *Server) githubMode() string {
	if s.Cfg.GitHubTokenFile != "" {
		return githubconnect.ModeOperatorToken
	}
	return githubconnect.ModeDeviceFlow
}

// configureGithubServices performs no network calls and starts no goroutines.
// The executable owns service lifetime; tests may inject their own provider.
func (s *Server) configureGithubServices() { s.installGithubServices(githubSeams{}) }

// installGithubServices builds the provider and, in device_flow mode with
// GitHub enabled, the connection manager that supplies its credential and
// allowlist. A manager built earlier is closed first.
func (s *Server) installGithubServices(seams githubSeams) {
	if s.GithubConnection != nil {
		s.GithubConnection.Close()
		s.GithubConnection = nil
	}
	options := githubprovider.Options{
		Enabled: s.Cfg.GitHubEnabled, TokenFile: s.Cfg.GitHubTokenFile, Repositories: s.Cfg.GitHubRepositories,
		HTTPClient: seams.provider.HTTPClient, Endpoint: seams.provider.Endpoint,
	}
	var manager *githubconnect.Manager
	if s.Cfg.GitHubEnabled && s.githubMode() == githubconnect.ModeDeviceFlow {
		built, err := githubconnect.New(githubconnect.Options{
			ClientID: s.Cfg.GitHubAppClientID, AppSlug: s.Cfg.GitHubAppSlug, StateDir: s.Cfg.GitHubStateDir,
			Ceiling:    s.Cfg.GitHubRepositories,
			HTTPClient: seams.connect.HTTPClient, WebOrigin: seams.connect.WebOrigin, APIOrigin: seams.connect.APIOrigin, Now: seams.connect.Now,
		})
		if err != nil {
			// Configuration is validated at startup, so this is not expected;
			// sign-in then reports itself unavailable.
			log.Printf(`{"level":"warn","msg":"github connection unavailable","code":%q}`, githubconnect.Code(err))
		} else {
			// The allowlist comes from the manager; the configured list is
			// only its ceiling, applied there.
			manager, options.Source, options.Repositories = built, built, nil
		}
	}
	provider := githubprovider.New(options)
	if manager != nil {
		manager.OnRepositories(provider.SetRepositories)
	}
	s.GithubConnection = manager
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
// The connection manager starts first: the services read credentials from it.
func (s *Server) StartGithubServices(ctx context.Context) {
	if !s.Cfg.GitHubEnabled {
		return
	}
	if s.GithubConnection != nil {
		s.GithubConnection.Start(ctx)
	}
	if s.Github != nil {
		s.Github.Start(ctx)
	}
	if s.GithubAudits != nil {
		s.GithubAudits.Start(ctx)
	}
}

// StopGithubServices stops audits and refreshes, then the connection manager
// they read credentials from. It is safe to call twice or without Start.
func (s *Server) StopGithubServices(ctx context.Context) error {
	var err error
	if s.GithubAudits != nil {
		err = s.GithubAudits.Close(ctx)
	}
	if s.Github != nil {
		s.Github.Close()
	}
	if s.GithubConnection != nil {
		s.GithubConnection.Close()
	}
	return err
}
