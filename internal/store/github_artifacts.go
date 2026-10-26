package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) LinkGithubArtifact(ctx context.Context, taskID, repository string, number int, url string, expected int64, actorID string, override bool) (GithubArtifact, Task, error) {
	var artifactID string
	if expected <= 0 {
		return GithubArtifact{}, Task{}, ErrPrecondition
	}
	if number <= 0 || url == "" {
		return GithubArtifact{}, Task{}, invalid("valid pull request identity is required", nil)
	}
	repository = strings.ToLower(repository)
	err := s.withImmediateTx(ctx, func(q dependencySQL) error {
		task, err := resolveDependencyTask(ctx, q, taskID)
		if err != nil {
			return err
		}
		if task.Version != expected {
			return conflict("task has changed", map[string]any{"current_version": task.Version})
		}
		if !override {
			if err = dependencyClaimConflict(ctx, q, task.ID, actorID); err != nil {
				return err
			}
		}
		if err = githubRepositorySelected(ctx, q, task.ProjectID, repository); err != nil {
			return err
		}
		err = q.QueryRowContext(ctx, `SELECT id FROM github_artifacts WHERE project_id=? AND repository=? AND number=?`, task.ProjectID, repository, number).Scan(&artifactID)
		if errors.Is(err, sql.ErrNoRows) {
			artifactID = newID()
			_, err = q.ExecContext(ctx, `INSERT INTO github_artifacts(id,project_id,repository,number,url,created_at) VALUES(?,?,?,?,?,?)`, artifactID, task.ProjectID, repository, number, url, now())
		}
		if err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO task_artifacts(task_id,artifact_id,created_at,created_by) VALUES(?,?,?,NULLIF(?,''))`, task.ID, artifactID, now(), actorID)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		if changed == 0 {
			return nil
		}
		return githubLinkEvent(ctx, q, task, artifactID, actorID, "task.artifact_linked")
	})
	if err != nil {
		return GithubArtifact{}, Task{}, err
	}
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return GithubArtifact{}, Task{}, err
	}
	artifact, err := s.GetGithubArtifact(ctx, task.ID, artifactID)
	return artifact, task, err
}
func githubLinkEvent(ctx context.Context, q dependencySQL, task dependencyTask, artifactID, actorID, kind string) error {
	if _, err := q.ExecContext(ctx, `UPDATE tasks SET version=version+1,updated_at=? WHERE id=? AND version=? AND deleted_at IS NULL`, now(), task.ID, task.Version); err != nil {
		return err
	}
	_, err := insertEvent(ctx, q, kind, actorID, task.ProjectID, task.ID, map[string]any{"artifact_id": artifactID, "version": task.Version + 1})
	return err
}
func (s *Store) DetachGithubArtifact(ctx context.Context, taskID, artifactID string, expected int64, actorID string, override bool) (Task, error) {
	if expected <= 0 {
		return Task{}, ErrPrecondition
	}
	err := s.withImmediateTx(ctx, func(q dependencySQL) error {
		task, err := resolveDependencyTask(ctx, q, taskID)
		if err != nil {
			return err
		}
		if task.Version != expected {
			return conflict("task has changed", map[string]any{"current_version": task.Version})
		}
		if !override {
			if err = dependencyClaimConflict(ctx, q, task.ID, actorID); err != nil {
				return err
			}
		}
		result, err := q.ExecContext(ctx, `DELETE FROM task_artifacts WHERE task_id=? AND artifact_id=? AND EXISTS(SELECT 1 FROM github_artifacts a WHERE a.id=artifact_id AND a.project_id=?)`, task.ID, artifactID, task.ProjectID)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		if changed == 0 {
			return ErrNotFound
		}
		return githubLinkEvent(ctx, q, task, artifactID, actorID, "task.artifact_detached")
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, taskID)
}

const githubArtifactColumns = `a.id,ta.task_id,a.project_id,a.repository,a.number,a.url,a.created_at,a.verification,a.error_code,a.snapshot_json,COALESCE(g.version,1),a.cache_selection_version,EXISTS(SELECT 1 FROM github_project_repositories r WHERE r.project_id=a.project_id AND r.repository=a.repository)`

func scanGithubArtifact(row interface{ Scan(...any) error }) (GithubArtifact, error) {
	a := GithubArtifact{Kind: "pull_request", Provider: "github"}
	var snapshot sql.NullString
	var cached sql.NullInt64
	var selected bool
	if err := row.Scan(&a.ID, &a.TaskID, &a.ProjectID, &a.Repository, &a.Number, &a.URL, &a.CreatedAt, &a.Verification, &a.ErrorCode, &snapshot, &a.SelectionVersion, &cached, &selected); err != nil {
		return a, err
	}
	if selected && cached.Valid && cached.Int64 == a.SelectionVersion && snapshot.Valid && a.Verification != "unavailable" {
		var snap GithubSnapshot
		if err := json.Unmarshal([]byte(snapshot.String), &snap); err != nil {
			return a, err
		}
		at, err := time.Parse(time.RFC3339Nano, snap.FetchedAt)
		snap.Stale = snap.Stale || err != nil || time.Since(at) > 2*time.Minute || a.ErrorCode != ""
		a.Snapshot = &snap
	} else if !selected {
		a.Verification = "unavailable"
		a.ErrorCode = "repository_not_selected"
	} else if a.Verification == "verified" {
		a.Verification = "unverified"
	}
	if a.ErrorCode != "" {
		a.ErrorMessage = "GitHub metadata is unavailable; check repository access and refresh."
	}
	return a, nil
}
func (s *Store) ListGithubArtifacts(ctx context.Context, taskID string) ([]GithubArtifact, error) {
	artifacts, err := s.ListGithubArtifactsForTasks(ctx, []string{taskID})
	if err != nil {
		return nil, err
	}
	return artifacts, nil
}
func (s *Store) GetGithubArtifact(ctx context.Context, taskID, artifactID string) (GithubArtifact, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+githubArtifactColumns+` FROM task_artifacts ta JOIN github_artifacts a ON a.id=ta.artifact_id JOIN tasks t ON t.id=ta.task_id AND t.project_id=a.project_id LEFT JOIN github_project_settings g ON g.project_id=a.project_id WHERE ta.task_id=? AND a.id=? AND t.deleted_at IS NULL`, taskID, artifactID)
	a, err := scanGithubArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return a, err
}

// ListGithubArtifactsForTasks performs one local batch query. Authorization is
// performed by the caller before invocation; project equality remains in SQL.
func (s *Store) ListGithubArtifactsForTasks(ctx context.Context, taskIDs []string) ([]GithubArtifact, error) {
	result := []GithubArtifact{}
	if len(taskIDs) == 0 {
		return result, nil
	}
	if len(taskIDs) > 200 {
		return nil, invalid("at most 200 tasks may be queried", nil)
	}
	args := make([]any, len(taskIDs))
	for i, id := range taskIDs {
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+githubArtifactColumns+` FROM task_artifacts ta JOIN github_artifacts a ON a.id=ta.artifact_id JOIN tasks t ON t.id=ta.task_id AND t.project_id=a.project_id LEFT JOIN github_project_settings g ON g.project_id=a.project_id WHERE t.deleted_at IS NULL AND ta.task_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`) ORDER BY ta.task_id,a.created_at,a.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanGithubArtifact(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
func GithubArtifactState(a GithubArtifact) string {
	if a.Verification == "unavailable" {
		return "unavailable"
	}
	if a.Snapshot == nil {
		return "unverified"
	}
	s := a.Snapshot
	if s.Stale || a.ErrorCode != "" {
		return "unknown"
	}
	if s.State == "merged" {
		return "merged"
	}
	if s.State == "closed" {
		return "closed"
	}
	if s.Partial && s.ChecksState != "failing" && s.ChecksState != "pending" {
		return "unknown"
	}
	switch s.ChecksState {
	case "failing", "pending", "passing", "unavailable":
		return s.ChecksState
	default:
		return "unknown"
	}
}
func GithubSummaries(taskIDs []string, artifacts []GithubArtifact) []GithubSummary {
	result := make([]GithubSummary, 0, len(taskIDs))
	group := map[string][]GithubArtifact{}
	for _, a := range artifacts {
		group[a.TaskID] = append(group[a.TaskID], a)
	}
	rank := map[string]int{"failing": 9, "pending": 8, "unavailable": 7, "unverified": 6, "unknown": 5, "passing": 4, "closed": 3, "merged": 2}
	for _, id := range taskIDs {
		summary := GithubSummary{TaskID: id, State: "unknown", Count: len(group[id])}
		best := 0
		for _, a := range group[id] {
			state := GithubArtifactState(a)
			if rank[state] > best {
				summary.State = state
				best = rank[state]
			}
		}
		if summary.Count == 1 {
			summary.Label = fmt.Sprintf("#%d", group[id][0].Number)
		} else if summary.Count > 1 {
			summary.Label = fmt.Sprintf("%d PRs", summary.Count)
		}
		result = append(result, summary)
	}
	return result
}
