package httpapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

func (g *GithubAuditService) captureBaseline(ctx context.Context, repo store.GithubRepository, baseline *store.GithubAuditBaseline, e *store.GithubAuditEvidence, check func() error) (store.GithubAuditRepository, error) {
	if baseline == nil || baseline.Repository != repo.Repository {
		resolved, err := g.provider.Resolve(ctx, repo.Repository, repo.Ref)
		return store.GithubAuditRepository{Repository: repo.Repository, Ref: repo.Ref, SHA: resolved.SHA, RepositoryID: resolved.RepositoryID}, err
	}
	if err := check(); err != nil {
		return store.GithubAuditRepository{}, err
	}
	pr, err := g.provider.FetchPullRequest(ctx, repo.Repository, baseline.PullRequest)
	if fence := check(); fence != nil {
		return store.GithubAuditRepository{}, fence
	}
	if err != nil {
		return store.GithubAuditRepository{}, err
	}
	ref := fmt.Sprintf("refs/pull/%d/head", baseline.PullRequest)
	pin := store.GithubAuditRepository{Repository: repo.Repository, Ref: ref, SHA: pr.HeadSHA, RepositoryID: pr.RepositoryID, PullRequest: baseline.PullRequest}
	raw, _ := json.Marshal(pr)
	tasks, criteria := []string{}, []string{}
	for _, task := range e.Tasks {
		tasks = append(tasks, task.ID)
		criteria = append(criteria, conditionIDs(task)...)
	}
	e.Items = append(e.Items, store.GithubAuditItem{ID: "github:baseline-pr", Kind: "pull_request", EvidenceClass: "pull_request", SourceRole: "baseline", Title: pr.Title, URL: pr.URL, Repository: repo.Repository, Ref: ref, SHA: pr.HeadSHA, Text: string(raw), TaskIDs: tasks, CriterionIDs: criteria})
	for i, observed := range pr.Checks {
		if i >= 100 {
			e.Omissions = append(e.Omissions, "Additional baseline checks omitted by limit")
			break
		}
		raw, _ = json.Marshal(observed)
		e.Items = append(e.Items, store.GithubAuditItem{ID: fmt.Sprintf("github:baseline-check:%d", i+1), Kind: "check", EvidenceClass: "observed_check", SourceRole: "baseline", Title: observed.Name, URL: observed.URL, Repository: repo.Repository, Ref: ref, SHA: observed.SHA, Text: string(raw), TaskIDs: tasks, CriterionIDs: criteria})
	}
	return pin, nil
}
func (g *GithubAuditService) currentBaseline(ctx context.Context, repo store.GithubAuditRepository) (githubprovider.ResolvedRef, error) {
	if repo.PullRequest == 0 {
		return g.provider.Resolve(ctx, repo.Repository, repo.Ref)
	}
	current, err := g.provider.FetchPullRequest(ctx, repo.Repository, repo.PullRequest)
	return githubprovider.ResolvedRef{SHA: current.HeadSHA, RepositoryID: current.RepositoryID}, err
}
