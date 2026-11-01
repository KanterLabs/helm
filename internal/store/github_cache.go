package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// SaveGithubArtifactSnapshot commits the entire SHA-consistent provider result
// behind the captured selection generation. An obsolete request cannot revive
// a deselected repository. Permission failures erase disposable private cache.
func (s *Store) SaveGithubArtifactSnapshot(ctx context.Context, artifactID string, selectionVersion int64, snapshot *GithubSnapshot, errorCode string, hide bool) error {
	if snapshot != nil && (snapshot.RepositoryID <= 0 || snapshot.HeadSHA == "" || snapshot.BaseSHA == "" || len(snapshot.Title) > 4096 || len(snapshot.Checks) > 200) {
		return invalid("invalid GitHub snapshot", nil)
	}
	return s.withImmediateTx(ctx, func(q dependencySQL) error {
		var projectID, repository string
		var number int
		if err := q.QueryRowContext(ctx, `SELECT project_id,repository,number FROM github_artifacts WHERE id=?`, artifactID).Scan(&projectID, &repository, &number); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		selection, err := githubSelection(ctx, q, projectID)
		if err != nil {
			return err
		}
		if selection.Version != selectionVersion {
			return conflict("repository selection changed during refresh", nil)
		}
		if err = githubRepositorySelected(ctx, q, projectID, repository); err != nil {
			return err
		}
		next := time.Now().UTC().Add(2*time.Minute + time.Duration(time.Now().UnixNano()%30000)*time.Millisecond).Format(time.RFC3339Nano)
		if snapshot == nil {
			if hide {
				_, err = q.ExecContext(ctx, `UPDATE github_artifacts SET verification='unavailable',snapshot_json=NULL,error_code=?,cache_selection_version=?,refresh_after=? WHERE id=?`, errorCode, selectionVersion, next, artifactID)
			} else {
				_, err = q.ExecContext(ctx, `UPDATE github_artifacts SET error_code=?,refresh_after=? WHERE id=?`, errorCode, next, artifactID)
			}
			return err
		}
		// URL spelling/renames converge only after GitHub verifies immutable ID.
		var canonicalID string
		err = q.QueryRowContext(ctx, `SELECT id FROM github_artifacts WHERE project_id=? AND repository_id=? AND number=? AND id<>?`, projectID, snapshot.RepositoryID, number, artifactID).Scan(&canonicalID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if canonicalID != "" {
			if _, err = q.ExecContext(ctx, `INSERT OR IGNORE INTO task_artifacts(task_id,artifact_id,created_at,created_by) SELECT task_id,?,created_at,created_by FROM task_artifacts WHERE artifact_id=?`, canonicalID, artifactID); err != nil {
				return err
			}
			if _, err = q.ExecContext(ctx, `DELETE FROM github_artifacts WHERE id=?`, artifactID); err != nil {
				return err
			}
			artifactID = canonicalID
		}
		if snapshot.Checks == nil {
			snapshot.Checks = []GithubCheck{}
		}
		data, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE github_artifacts SET repository_id=?,verification='verified',snapshot_json=?,cache_selection_version=?,error_code='',refresh_after=? WHERE id=?`, snapshot.RepositoryID, string(data), selectionVersion, next, artifactID)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE github_artifacts SET snapshot_json=NULL,cache_selection_version=NULL,verification='unverified' WHERE snapshot_json IS NOT NULL AND id NOT IN (SELECT id FROM github_artifacts WHERE snapshot_json IS NOT NULL ORDER BY CASE WHEN id=? THEN 0 ELSE 1 END,json_extract(snapshot_json,'$.fetched_at') DESC,id LIMIT 512)`, artifactID)
		return err
	})
}

// GithubRefreshCandidates returns a fixed-budget set of linked selected PRs.
// Each artifact appears once even when several tasks reference it.
func (s *Store) GithubRefreshCandidates(ctx context.Context, limit int) ([]GithubArtifact, error) {
	if limit < 1 || limit > 8 {
		limit = 8
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id,MIN(ta.task_id) FROM github_artifacts a JOIN task_artifacts ta ON ta.artifact_id=a.id JOIN tasks t ON t.id=ta.task_id AND t.deleted_at IS NULL JOIN github_project_repositories r ON r.project_id=a.project_id AND r.repository=a.repository WHERE (a.refresh_after='' OR julianday(a.refresh_after)<=julianday('now')) AND (a.snapshot_json IS NULL OR json_extract(a.snapshot_json,'$.state')='open') GROUP BY a.id ORDER BY a.refresh_after,a.id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	type target struct{ id, task string }
	targets := []target{}
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.task); err != nil {
			rows.Close()
			return nil, err
		}
		targets = append(targets, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []GithubArtifact{}
	for _, t := range targets {
		a, err := s.GetGithubArtifact(ctx, t.task, t.id)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, nil
}
