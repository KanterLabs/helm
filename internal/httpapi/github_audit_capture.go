package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

func (g *GithubAuditService) capture(ctx context.Context, job store.GithubAuditJob, c store.GithubAuditCapture, check func() error) (store.GithubAuditEvidence, error) {
	return g.captureWithBudget(ctx, job, c, check, defaultRetrievalBudget())
}

func (g *GithubAuditService) captureWithBudget(ctx context.Context, job store.GithubAuditJob, c store.GithubAuditCapture, check func() error, budget store.GithubAuditRetrievalBudget) (store.GithubAuditEvidence, error) {
	budget = boundedRetrievalBudget(budget)
	ctx, cancel := retrievalDeadline(ctx, budget)
	defer cancel()
	e := store.GithubAuditEvidence{AssessmentVersion: c.AssessmentVersion, Columns: c.Columns, AuditID: job.AuditID, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano), Tasks: c.Tasks, Omissions: append([]string{}, c.Omissions...), Repositories: []store.GithubAuditRepository{}, Items: []store.GithubAuditItem{}}
	if c.AssessmentVersion == store.GithubAssessmentVersion {
		for _, task := range c.Tasks {
			raw, _ := json.Marshal(map[string]any{"task_id": task.ID, "version": task.Version, "context": task.Context, "checklist_policy": task.ChecklistPolicy})
			e.Items = append(e.Items, store.GithubAuditItem{ID: "task:context:" + task.ID, Kind: "task_context", EvidenceClass: "task_fact", SourceRole: "task_context", Title: task.Key, Text: string(raw), TaskIDs: []string{task.ID}, CriterionIDs: conditionIDs(task)})
		}
	}
	retrieval := newRetrievalRun(g, ctx, check, &e, budget)
	retrieval.job = job
	sources := []*retrievalSource{}
	selected := map[string]*retrievalSource{}
	for _, repo := range c.Request.Repositories {
		if err := retrieval.fence(); err != nil {
			return e, err
		}
		pinned, err := g.captureBaseline(ctx, repo, c.Request.Baseline, &e, retrieval.fence)
		if err != nil {
			return e, err
		}
		if err = retrieval.fence(); err != nil {
			return e, err
		}
		e.Repositories = append(e.Repositories, pinned)
		source := &retrievalSource{repository: repo.Repository, ref: pinned.Ref, sha: pinned.SHA, role: "baseline", tasks: c.Tasks}
		selected[repo.Repository] = source
		sources = append(sources, source)
	}
	seenPR := map[string]bool{}
	prItems := map[string][]int{}
	prSources := map[string]*retrievalSource{}
	prCount, prAttempts, checkCount := 0, 0, 0
	for _, task := range c.Tasks {
		if err := retrieval.fence(); err != nil {
			return e, err
		}
		artifacts, err := g.store.ListGithubArtifacts(ctx, task.ID)
		if err != nil {
			return e, err
		}
		for _, artifact := range artifacts {
			key := fmt.Sprintf("%s/%d", artifact.Repository, artifact.Number)
			if seenPR[key] {
				for _, index := range prItems[key] {
					e.Items[index].TaskIDs = appendUnique(e.Items[index].TaskIDs, task.ID)
					e.Items[index].CriterionIDs = appendUnique(e.Items[index].CriterionIDs, conditionIDs(task)...)
				}
				if source := prSources[key]; source != nil {
					source.tasks = append(source.tasks, task)
				} else {
					retrieval.linkedGap(task, artifact.Repository, "linked_source_unavailable")
				}
				continue
			}
			seenPR[key] = true
			if selected[artifact.Repository] == nil {
				retrieval.linkedGap(task, artifact.Repository, "linked_repository_outside_scope")
				e.Omissions = append(e.Omissions, "Linked PR repository is outside audit scope")
				continue
			}
			if prAttempts >= 20 {
				retrieval.linkedGap(task, artifact.Repository, "linked_pr_budget_exhausted")
				e.Omissions = append(e.Omissions, "Additional linked PR omitted by capture limit")
				continue
			}
			if err = retrieval.fence(); err != nil {
				return e, err
			}
			prAttempts++
			pr, err := g.provider.FetchPullRequest(ctx, artifact.Repository, artifact.Number)
			if fence := retrieval.fence(); fence != nil {
				return e, fence
			}
			if err != nil {
				retrieval.linkedGap(task, artifact.Repository, "linked_source_unavailable")
				e.Omissions = append(e.Omissions, "Linked PR unavailable: "+key)
				continue
			}
			seenPR[key] = true
			raw, _ := json.Marshal(pr)
			prCount++
			ref := fmt.Sprintf("refs/pull/%d/head", pr.Number)
			e.Items = append(e.Items, store.GithubAuditItem{ID: fmt.Sprintf("github:pr:%d", prCount), Kind: "pull_request", EvidenceClass: "pull_request", Title: pr.Title, URL: pr.URL, Repository: pr.Repository, Ref: ref, SHA: pr.HeadSHA, SourceRole: "linked_pr", Text: string(raw), TaskIDs: []string{task.ID}, CriterionIDs: conditionIDs(task)})
			prItems[key] = append(prItems[key], len(e.Items)-1)
			if pr.Partial || pr.ChecksState == "unavailable" || pr.ChecksState == "unknown" {
				e.Omissions = append(e.Omissions, "Linked PR checks or reviews have incomplete coverage: "+key)
			}
			for _, observed := range pr.Checks {
				if checkCount >= 100 {
					e.Omissions = append(e.Omissions, "Additional checks omitted by capture limit")
					break
				}
				checkCount++
				raw, _ := json.Marshal(observed)
				e.Items = append(e.Items, store.GithubAuditItem{ID: fmt.Sprintf("github:check:%d", checkCount), Kind: "check", EvidenceClass: "observed_check", Title: observed.Name, URL: observed.URL, Repository: pr.Repository, Ref: ref, SHA: observed.SHA, SourceRole: "linked_pr", Text: string(raw), TaskIDs: []string{task.ID}, CriterionIDs: conditionIDs(task)})
				prItems[key] = append(prItems[key], len(e.Items)-1)
			}
			source := &retrievalSource{repository: pr.Repository, ref: ref, sha: pr.HeadSHA, role: "linked_pr", tasks: []store.GithubAuditTask{task}}
			if discovery, ok := g.provider.(githubprovider.SourceDiscovery); ok {
				if err = retrieval.fence(); err != nil {
					return e, err
				}
				paths, partial, err := discovery.PullRequestPaths(ctx, pr.Repository, pr.Number, pr.HeadSHA)
				if fence := retrieval.fence(); fence != nil {
					return e, fence
				}
				if err == nil {
					source.preferred = paths
					selected[pr.Repository].preferred = appendUnique(selected[pr.Repository].preferred, paths...)
				}
				if err != nil || partial {
					e.Omissions = append(e.Omissions, "Linked PR change hints incomplete: "+key)
				}
			}
			prSources[key] = source
			sources = append(sources, source)
		}
	}
	if err := retrieval.retrieve(sources); err != nil {
		return e, err
	}
	if len(e.Items) == 0 {
		e.Omissions = append(e.Omissions, "No readable source evidence captured")
	}
	e.Partial = len(e.Omissions) > 0 || len(e.CriterionGaps) > 0
	if err := retrieval.fence(); err != nil {
		return e, err
	}
	if err := g.fresh(ctx, e); err != nil {
		return e, err
	}
	return e, retrieval.fence()
}

func conditionIDs(task store.GithubAuditTask) []string {
	ids := []string{}
	for _, condition := range task.Conditions {
		ids = append(ids, condition.ID)
	}
	return ids
}

// fresh rechecks exact branch/repository identities and all material PR/check
// observations. A failed remote check never authorizes an evidence-based move.
func (g *GithubAuditService) fresh(ctx context.Context, e store.GithubAuditEvidence) error {
	if len(e.Repositories) == 0 {
		return fmt.Errorf("missing source identity")
	}
	for _, repo := range e.Repositories {
		current, err := g.currentBaseline(ctx, repo)
		if err != nil {
			return err
		}
		if current.SHA != repo.SHA || current.RepositoryID != repo.RepositoryID {
			return fmt.Errorf("source branch changed")
		}
	}
	for _, item := range e.Items {
		if item.Kind != "pull_request" {
			continue
		}
		var old githubprovider.PullRequestSnapshot
		if err := json.Unmarshal([]byte(item.Text), &old); err != nil {
			return err
		}
		current, err := g.provider.FetchPullRequest(ctx, old.Repository, old.Number)
		if err != nil {
			return err
		}
		if current.HeadSHA != old.HeadSHA || current.BaseSHA != old.BaseSHA || current.MergeSHA != old.MergeSHA || current.State != old.State || current.Draft != old.Draft || current.ReviewState != old.ReviewState || current.ChecksState != old.ChecksState || current.Partial != old.Partial || (e.AssessmentVersion == 0 && current.Partial) {
			return fmt.Errorf("pull request evidence changed")
		}
		a, _ := json.Marshal(current.Checks)
		b, _ := json.Marshal(old.Checks)
		if string(a) != string(b) {
			return fmt.Errorf("check evidence changed")
		}
	}
	return nil
}
