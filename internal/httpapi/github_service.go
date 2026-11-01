package httpapi

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

type githubRefreshCall struct {
	done     chan struct{}
	err      error
	snapshot *store.GithubSnapshot
}

// GithubService owns bounded refresh work; construction starts no goroutines.
// Private cache reads are local and require a successful verification in this
// process/credential generation, so a restart never trusts cached permissions.
type GithubService struct {
	data     *store.Store
	provider *githubprovider.Client
	mu       sync.Mutex
	verified map[string]uint64
	inflight map[string]*githubRefreshCall
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewGithubService(data *store.Store, provider *githubprovider.Client) *GithubService {
	return &GithubService{data: data, provider: provider, verified: map[string]uint64{}, inflight: map[string]*githubRefreshCall{}}
}
func (g *GithubService) Provider() *githubprovider.Client {
	if g == nil {
		return nil
	}
	return g.provider
}
func (g *GithubService) Diagnostics(ctx context.Context, check bool) githubprovider.Diagnostics {
	if g == nil || g.provider == nil {
		return githubprovider.Diagnostics{State: "not_configured", Message: "Configure the server GitHub connection.", AllowedRepositories: []string{}, CheckedAt: time.Now().UTC()}
	}
	if check {
		return g.provider.Diagnostics(ctx)
	}
	return g.provider.CachedDiagnostics()
}
func (g *GithubService) repositoryAllowed(repository string) bool {
	if g == nil || g.provider == nil {
		return false
	}
	for _, allowed := range g.provider.AllowedRepositories() {
		if allowed == repository {
			return true
		}
	}
	return false
}

// SelectionAllowed revalidates project selection and current observed provider
// access without network I/O. Audit capture must still verify actual access.
func (g *GithubService) SelectionAllowed(ctx context.Context, projectID string) (store.GithubSelection, error) {
	if g == nil || g.provider == nil {
		return store.GithubSelection{}, &store.Error{Kind: store.ErrForbidden, Message: "GitHub integration is not configured"}
	}
	selection, err := g.data.GetGithubSelection(ctx, projectID)
	if err != nil {
		return selection, err
	}
	for _, repo := range selection.Repositories {
		if !g.repositoryAllowed(repo.Repository) || !g.provider.AccessAllowed(repo.Repository) {
			return selection, &store.Error{Kind: store.ErrForbidden, Message: "GitHub repository access must be checked before running an audit"}
		}
	}
	return selection, nil
}
func (g *GithubService) MaskArtifact(a store.GithubArtifact) store.GithubArtifact {
	if g == nil || g.provider == nil {
		a.Snapshot = nil
		a.Verification = "unavailable"
		a.ErrorCode = "not_configured"
		return a
	}
	access := g.provider.AccessAllowed(a.Repository)
	generation := g.provider.Generation()
	g.mu.Lock()
	verified := g.verified[a.ID]
	g.mu.Unlock()
	if !access || verified != generation {
		a.Snapshot = nil
		if !access {
			a.Verification = "unavailable"
			a.ErrorCode = "permission_required"
		} else {
			a.Verification = "unverified"
			a.ErrorCode = "verification_required"
		}
		a.ErrorMessage = "Check GitHub access and refresh this pull request."
	}
	return a
}
func (g *GithubService) Artifacts(ctx context.Context, taskID string) ([]store.GithubArtifact, error) {
	artifacts, err := g.data.ListGithubArtifacts(ctx, taskID)
	if err != nil {
		return nil, err
	}
	for i := range artifacts {
		artifacts[i] = g.MaskArtifact(artifacts[i])
	}
	return artifacts, nil
}
func (g *GithubService) Refresh(ctx context.Context, a store.GithubArtifact) (store.GithubArtifact, error) {
	if g == nil || g.provider == nil {
		return a, &store.Error{Kind: store.ErrForbidden, Message: "GitHub integration is not configured"}
	}
	if !g.repositoryAllowed(a.Repository) {
		return a, &store.Error{Kind: store.ErrForbidden, Message: "repository is outside the configured GitHub allowlist"}
	}
	selection, err := g.data.GetGithubSelection(ctx, a.ProjectID)
	if err != nil {
		return a, err
	}
	selected := false
	for _, repo := range selection.Repositories {
		if repo.Repository == a.Repository {
			selected = true
		}
	}
	if !selected {
		return a, &store.Error{Kind: store.ErrForbidden, Message: "repository is no longer selected for this project"}
	}
	key := fmt.Sprintf("%s:%d:%d", a.ID, selection.Version, g.provider.Generation())
	g.mu.Lock()
	if call, ok := g.inflight[key]; ok {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return a, ctx.Err()
		case <-call.done:
			if call.err != nil {
				return a, call.err
			}
			if call.snapshot != nil {
				a.Snapshot = call.snapshot
			}
			return g.refreshedArtifact(ctx, a)
		}
	}
	call := &githubRefreshCall{done: make(chan struct{})}
	g.inflight[key] = call
	g.mu.Unlock()
	// Request timeouts bound both manual and scheduled refresh. The complete
	// provider result is committed only if credentials and selection still match.
	fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	generation := g.provider.Generation()
	snapshot, fetchErr := g.provider.FetchPullRequest(fetchCtx, a.Repository, a.Number)
	cancel()
	code := githubprovider.ErrorCode(fetchErr)
	if g.provider.Generation() != generation {
		snapshot = githubprovider.PullRequestSnapshot{}
		code = "permission_required"
		fetchErr = &githubprovider.Error{Code: code}
	}
	var normalized *store.GithubSnapshot
	if fetchErr == nil {
		checks := make([]store.GithubCheck, 0, len(snapshot.Checks))
		for _, c := range snapshot.Checks {
			checks = append(checks, store.GithubCheck{Name: c.Name, State: c.State, Conclusion: c.Conclusion, URL: c.URL, SHA: c.SHA})
		}
		normalized = &store.GithubSnapshot{RepositoryID: snapshot.RepositoryID, Title: snapshot.Title, State: snapshot.State, Draft: snapshot.Draft, HeadSHA: snapshot.HeadSHA, BaseSHA: snapshot.BaseSHA, MergeSHA: snapshot.MergeSHA, ChecksState: snapshot.ChecksState, Checks: checks, ReviewState: snapshot.ReviewState, FetchedAt: snapshot.FetchedAt.UTC().Format(time.RFC3339Nano), Stale: snapshot.Stale, Partial: snapshot.Partial}
	}
	hide := code == "permission_required" || code == "not_configured" || code == "repository_not_allowed"
	saveErr := g.data.SaveGithubArtifactSnapshot(ctx, a.ID, selection.Version, normalized, code, hide)
	if saveErr == nil && normalized != nil && g.provider.Generation() == generation {
		// Canonical verification can merge URL aliases, so find the surviving ID.
		artifacts, listErr := g.data.ListGithubArtifacts(ctx, a.TaskID)
		if listErr == nil {
			g.mu.Lock()
			for _, artifact := range artifacts {
				if artifact.Snapshot != nil && artifact.Snapshot.RepositoryID == snapshot.RepositoryID && artifact.Number == a.Number {
					if len(g.verified) >= 512 {
						g.verified = map[string]uint64{}
					}
					g.verified[artifact.ID] = generation
				}
			}
			g.mu.Unlock()
		}
	}
	g.mu.Lock()
	call.err = saveErr
	call.snapshot = normalized
	delete(g.inflight, key)
	close(call.done)
	g.mu.Unlock()
	if saveErr != nil {
		return a, saveErr
	}
	if normalized != nil {
		a.Snapshot = normalized
	}
	return g.refreshedArtifact(ctx, a)
}
func (g *GithubService) refreshedArtifact(ctx context.Context, a store.GithubArtifact) (store.GithubArtifact, error) {
	refreshed, err := g.data.GetGithubArtifact(ctx, a.TaskID, a.ID)
	if err != nil {
		artifacts, listErr := g.data.ListGithubArtifacts(ctx, a.TaskID)
		if listErr == nil {
			for _, candidate := range artifacts {
				if candidate.Number == a.Number && candidate.Snapshot != nil && a.Snapshot != nil && candidate.Snapshot.RepositoryID == a.Snapshot.RepositoryID {
					refreshed = candidate
					err = nil
					break
				}
			}
		}
	}
	return g.MaskArtifact(refreshed), err
}
func (g *GithubService) Start(parent context.Context) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.cancel != nil {
		g.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	g.cancel = cancel
	g.done = make(chan struct{})
	done := g.done
	g.mu.Unlock()
	go func() {
		defer close(done)
		interval := 2*time.Minute + time.Duration(time.Now().UnixNano()%30000)*time.Millisecond
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				budget, cancel := context.WithTimeout(ctx, 90*time.Second)
				candidates, err := g.data.GithubRefreshCandidates(budget, 8)
				if err == nil {
					for _, a := range candidates {
						if budget.Err() != nil {
							break
						}
						_, _ = g.Refresh(budget, a)
					}
				}
				cancel()
			}
		}
	}()
}
func (g *GithubService) Close() {
	if g == nil {
		return
	}
	g.mu.Lock()
	cancel, done := g.cancel, g.done
	g.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
