package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

func (g *GithubAuditService) capture(ctx context.Context, job store.GithubAuditJob, c store.GithubAuditCapture, check func() error) (store.GithubAuditEvidence, error) {
	e := store.GithubAuditEvidence{AuditID: job.AuditID, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano), Tasks: c.Tasks, Omissions: append([]string{}, c.Omissions...), Repositories: []store.GithubAuditRepository{}, Items: []store.GithubAuditItem{}}
	bytes, files, fileRequests := 0, 0, 0
	selected := map[string]bool{}
	seenSources := map[string]bool{}
	addFiles := func(repo, sha string) error {
		key := repo + "/" + sha
		if seenSources[key] {
			return nil
		}
		seenSources[key] = true
		if err := check(); err != nil {
			return err
		}
		tree, partial, err := g.provider.ListFiles(ctx, repo, sha)
		if err != nil {
			e.Omissions = append(e.Omissions, "Source tree unavailable: "+repo+"@"+sha)
			return nil
		}
		if partial {
			e.Omissions = append(e.Omissions, "Source tree excludes unsupported files or truncated entries: "+repo)
		}
		// Explicit paths in task goals/criteria are ranked first. Every omitted
		// source file contributes visible coverage limits, including file budgets.
		relevance := func(path string) int {
			score := 0
			lower := strings.ToLower(path)
			for _, task := range c.Tasks {
				description := strings.ToLower(task.Title + " " + task.Description + " " + strings.Join(task.Criteria, " "))
				if strings.Contains(description, lower) {
					score += 100
				}
				for _, word := range strings.Fields(description) {
					if len(word) >= 5 && strings.Contains(lower, strings.Trim(word, ".,:;()")) {
						score++
					}
				}
			}
			return score
		}
		sort.SliceStable(tree, func(i, j int) bool {
			a, b := relevance(tree[i].Path), relevance(tree[j].Path)
			if a != b {
				return a > b
			}
			return tree[i].Path < tree[j].Path
		})
		omitted := 0
		for _, entry := range tree {
			if files >= 48 || fileRequests >= 96 || bytes+int(entry.Size) > 160*1024 {
				omitted++
				continue
			}
			if err = check(); err != nil {
				return err
			}
			fileRequests++
			file, readErr := g.provider.ReadFile(ctx, repo, sha, entry.Path)
			if readErr != nil {
				e.OmittedFiles++
				e.Omissions = append(e.Omissions, "File unavailable: "+repo+":"+entry.Path)
				continue
			}
			if file.SHA != entry.SHA || file.CommitSHA != sha || len(file.Text)+bytes > 160*1024 {
				e.OmittedFiles++
				e.Omissions = append(e.Omissions, "File identity or size limit: "+repo+":"+entry.Path)
				continue
			}
			bytes += len(file.Text)
			files++
			e.Items = append(e.Items, store.GithubAuditItem{ID: fmt.Sprintf("github:file:%d", files), Kind: "file", Title: entry.Path, URL: file.URL, Repository: repo, SHA: sha, BlobSHA: file.SHA, Path: entry.Path, StartLine: 1, EndLine: strings.Count(file.Text, "\n") + 1, Text: file.Text})
		}
		e.OmittedFiles += omitted
		if omitted > 0 {
			e.Omissions = append(e.Omissions, fmt.Sprintf("%d files omitted by capture limits: %s@%s", omitted, repo, sha))
		}
		return nil
	}
	for _, repo := range c.Request.Repositories {
		if err := check(); err != nil {
			return e, err
		}
		resolved, err := g.provider.Resolve(ctx, repo.Repository, repo.Ref)
		if err != nil {
			return e, err
		}
		selected[repo.Repository] = true
		e.Repositories = append(e.Repositories, store.GithubAuditRepository{Repository: repo.Repository, Ref: repo.Ref, SHA: resolved.SHA, RepositoryID: resolved.RepositoryID})
		if err = addFiles(repo.Repository, resolved.SHA); err != nil {
			return e, err
		}
	}
	seenPR := map[string]bool{}
	prItems := map[string][]int{}
	prCount, checkCount := 0, 0
	for _, task := range c.Tasks {
		artifacts, err := g.store.ListGithubArtifacts(ctx, task.ID)
		if err != nil {
			return e, err
		}
		for _, artifact := range artifacts {
			key := fmt.Sprintf("%s/%d", artifact.Repository, artifact.Number)
			if seenPR[key] {
				for _, index := range prItems[key] {
					e.Items[index].TaskIDs = append(e.Items[index].TaskIDs, task.ID)
				}
				continue
			}
			seenPR[key] = true
			if !selected[artifact.Repository] {
				e.Omissions = append(e.Omissions, "Linked PR repository is outside audit scope")
				continue
			}
			if prCount >= 20 {
				e.Omissions = append(e.Omissions, "Additional linked PR omitted by capture limit")
				continue
			}
			if err = check(); err != nil {
				return e, err
			}
			pr, err := g.provider.FetchPullRequest(ctx, artifact.Repository, artifact.Number)
			if err != nil {
				e.Omissions = append(e.Omissions, "Linked PR unavailable: "+key)
				continue
			}
			raw, _ := json.Marshal(pr)
			prCount++
			e.Items = append(e.Items, store.GithubAuditItem{ID: fmt.Sprintf("github:pr:%d", prCount), Kind: "pull_request", Title: pr.Title, URL: pr.URL, Repository: pr.Repository, SHA: pr.HeadSHA, Text: string(raw), TaskIDs: []string{task.ID}})
			prItems[key] = append(prItems[key], len(e.Items)-1)
			if pr.Partial || pr.ChecksState == "unavailable" || pr.ChecksState == "unknown" {
				e.Omissions = append(e.Omissions, "Linked PR checks or reviews have incomplete coverage: "+key)
			}
			for _, check := range pr.Checks {
				if checkCount >= 100 {
					e.Omissions = append(e.Omissions, "Additional checks omitted by capture limit")
					break
				}
				checkCount++
				raw, _ := json.Marshal(check)
				e.Items = append(e.Items, store.GithubAuditItem{ID: fmt.Sprintf("github:check:%d", checkCount), Kind: "check", Title: check.Name, URL: check.URL, Repository: pr.Repository, SHA: check.SHA, Text: string(raw), TaskIDs: []string{task.ID}})
				prItems[key] = append(prItems[key], len(e.Items)-1)
			}
			if err = addFiles(pr.Repository, pr.HeadSHA); err != nil {
				return e, err
			}
		}
	}
	if len(e.Items) == 0 {
		e.Omissions = append(e.Omissions, "No readable source evidence captured")
	}
	e.Partial = len(e.Omissions) > 0
	if err := g.fresh(ctx, e); err != nil {
		return e, err
	}
	return e, nil
}

// fresh rechecks exact branch/repository identities and all material PR/check
// observations. A failed remote check never authorizes an evidence-based move.
func (g *GithubAuditService) fresh(ctx context.Context, e store.GithubAuditEvidence) error {
	if len(e.Repositories) == 0 {
		return fmt.Errorf("missing source identity")
	}
	for _, repo := range e.Repositories {
		current, err := g.provider.Resolve(ctx, repo.Repository, repo.Ref)
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
		if current.HeadSHA != old.HeadSHA || current.BaseSHA != old.BaseSHA || current.MergeSHA != old.MergeSHA || current.State != old.State || current.Draft != old.Draft || current.ReviewState != old.ReviewState || current.ChecksState != old.ChecksState || current.Partial {
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
