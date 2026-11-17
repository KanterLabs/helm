package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/KanterLabs/helm/internal/prlinks"
)

const MaxPullRequestLinksPerTask = 100

// PullRequestLink deliberately exposes only durable identity, never provider
// metadata, credentials, repository selections or disposable snapshots.
type PullRequestLink struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	Provider   string `json:"provider"`
	Host       string `json:"host"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	CreatedAt  string `json:"created_at"`
}

func (s *Store) ListPullRequestLinks(ctx context.Context, taskID string) ([]PullRequestLink, error) {
	links := []PullRequestLink{}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id,ta.task_id,a.url,a.created_at FROM task_artifacts ta JOIN github_artifacts a ON a.id=ta.artifact_id JOIN tasks t ON t.id=ta.task_id AND t.project_id=a.project_id WHERE ta.task_id=? AND t.deleted_at IS NULL ORDER BY ta.created_at,a.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var link PullRequestLink
		if err = rows.Scan(&link.ID, &link.TaskID, &link.URL, &link.CreatedAt); err != nil {
			return nil, err
		}
		identity, e := prlinks.Parse(link.URL)
		if e != nil {
			return nil, e
		}
		link.Provider, link.Host, link.Repository, link.Number, link.URL = identity.Provider, identity.Host, identity.Repository, identity.Number, identity.URL
		links = append(links, link)
	}
	return links, rows.Err()
}

func (s *Store) LinkPullRequest(ctx context.Context, taskID, rawURL string, expected int64, actorID string, override bool) (PullRequestLink, Task, error) {
	if expected <= 0 {
		return PullRequestLink{}, Task{}, ErrPrecondition
	}
	identity, err := prlinks.Parse(rawURL)
	if err != nil {
		return PullRequestLink{}, Task{}, invalid(err.Error(), nil)
	}
	var link PullRequestLink
	err = s.withImmediateTx(ctx, func(q dependencySQL) error {
		task, e := resolveDependencyTask(ctx, q, taskID)
		if e != nil {
			return e
		}
		if task.Version != expected {
			return conflict("task has changed", map[string]any{"current_version": task.Version})
		}
		if !override {
			if e = dependencyClaimConflict(ctx, q, task.ID, actorID); e != nil {
				return e
			}
		}
		link.TaskID = task.ID
		e = q.QueryRowContext(ctx, `SELECT id,created_at FROM github_artifacts WHERE project_id=? AND repository=? AND number=?`, task.ProjectID, identity.StorageRepository(), identity.Number).Scan(&link.ID, &link.CreatedAt)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			var exists int
			if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_artifacts WHERE task_id=? AND artifact_id=?`, task.ID, link.ID).Scan(&exists); e != nil {
				return e
			}
			if exists > 0 {
				return nil
			}
		}
		var count int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_artifacts WHERE task_id=?`, task.ID).Scan(&count); e != nil {
			return e
		}
		if count >= MaxPullRequestLinksPerTask {
			return invalid("a task may link at most 100 pull requests", nil)
		}
		if link.ID == "" {
			link.ID, link.CreatedAt = newID(), now()
			if _, e = q.ExecContext(ctx, `INSERT INTO github_artifacts(id,project_id,repository,number,url,created_at) VALUES(?,?,?,?,?,?)`, link.ID, task.ProjectID, identity.StorageRepository(), identity.Number, identity.URL, link.CreatedAt); e != nil {
				return e
			}
		}
		if _, e = q.ExecContext(ctx, `INSERT INTO task_artifacts(task_id,artifact_id,created_at,created_by) VALUES(?,?,?,NULLIF(?,''))`, task.ID, link.ID, now(), actorID); e != nil {
			return e
		}
		return githubLinkEvent(ctx, q, task, link.ID, actorID, "task.artifact_linked")
	})
	if err != nil {
		return PullRequestLink{}, Task{}, err
	}
	link.Provider, link.Host, link.Repository, link.Number, link.URL = identity.Provider, identity.Host, identity.Repository, identity.Number, identity.URL
	task, err := s.GetTask(ctx, link.TaskID)
	return link, task, err
}

func (s *Store) DetachPullRequest(ctx context.Context, taskID, linkID string, expected int64, actorID string, override bool) (Task, error) {
	// The historical detach operation reads only durable local tables and emits
	// the same task lifecycle event used for these plain links.
	return s.DetachGithubArtifact(ctx, taskID, linkID, expected, actorID, override)
}
