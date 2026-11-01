package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
)

func githubSelection(ctx context.Context, q dependencySQL, projectID string) (GithubSelection, error) {
	result := GithubSelection{Version: 1, Repositories: []GithubRepository{}}
	var exists int
	if err := q.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id=?`, projectID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	} else if err != nil {
		return result, err
	}
	err := q.QueryRowContext(ctx, `SELECT version FROM github_project_settings WHERE project_id=?`, projectID).Scan(&result.Version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	rows, err := q.QueryContext(ctx, `SELECT repository,ref FROM github_project_repositories WHERE project_id=? ORDER BY repository,ref`, projectID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var repo GithubRepository
		if err := rows.Scan(&repo.Repository, &repo.Ref); err != nil {
			return result, err
		}
		result.Repositories = append(result.Repositories, repo)
	}
	return result, rows.Err()
}
func (s *Store) GetGithubSelection(ctx context.Context, projectID string) (GithubSelection, error) {
	return githubSelection(ctx, s.DB, projectID)
}

func (s *Store) UpdateGithubSelection(ctx context.Context, projectID string, repositories []GithubRepository, expected int64) (GithubSelection, error) {
	if expected <= 0 {
		return GithubSelection{}, ErrPrecondition
	}
	if len(repositories) > 10 {
		return GithubSelection{}, invalid("select at most ten repositories", nil)
	}
	normalized := append([]GithubRepository{}, repositories...)
	seen := map[string]bool{}
	for i := range normalized {
		repo := &normalized[i]
		repo.Repository = strings.ToLower(strings.TrimSpace(repo.Repository))
		repo.Ref = strings.TrimSpace(repo.Ref)
		parts := strings.Split(repo.Repository, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repo.Repository, " ?#\\:\n\r\t") || repo.Ref == "" || len(repo.Ref) > 255 || strings.ContainsAny(repo.Ref, "\x00\r\n") {
			return GithubSelection{}, invalid("invalid repository or ref", nil)
		}
		key := repo.Repository + "\x00" + repo.Ref
		if seen[key] {
			return GithubSelection{}, invalid("duplicate repository and ref", nil)
		}
		seen[key] = true
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Repository == normalized[j].Repository {
			return normalized[i].Ref < normalized[j].Ref
		}
		return normalized[i].Repository < normalized[j].Repository
	})
	result := GithubSelection{}
	err := s.withImmediateTx(ctx, func(q dependencySQL) error {
		current, err := githubSelection(ctx, q, projectID)
		if err != nil {
			return err
		}
		if current.Version != expected {
			return conflict("repository selection has changed", map[string]any{"current_version": current.Version})
		}
		same := len(current.Repositories) == len(normalized)
		for i := range normalized {
			if !same {
				break
			}
			same = current.Repositories[i] == normalized[i]
		}
		if same {
			result = current
			return nil
		}
		if _, err = q.ExecContext(ctx, `INSERT INTO github_project_settings(project_id,version,updated_at) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET version=excluded.version,updated_at=excluded.updated_at`, projectID, current.Version+1, now()); err != nil {
			return err
		}
		if _, err = q.ExecContext(ctx, `DELETE FROM github_project_repositories WHERE project_id=?`, projectID); err != nil {
			return err
		}
		for _, repo := range normalized {
			if _, err = q.ExecContext(ctx, `INSERT INTO github_project_repositories(project_id,repository,ref) VALUES(?,?,?)`, projectID, repo.Repository, repo.Ref); err != nil {
				return err
			}
		}
		// A re-selection requires fresh verification, including repositories that
		// were removed and later restored. Old source remains inaccessible.
		if _, err = q.ExecContext(ctx, `UPDATE github_artifacts SET snapshot_json=NULL,cache_selection_version=NULL,verification='unverified',error_code='',refresh_after='' WHERE project_id=?`, projectID); err != nil {
			return err
		}
		result = GithubSelection{Version: current.Version + 1, Repositories: normalized}
		return nil
	})
	return result, err
}
func githubRepositorySelected(ctx context.Context, q dependencySQL, projectID, repository string) error {
	var found int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM github_project_repositories WHERE project_id=? AND repository=? LIMIT 1`, projectID, repository).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return forbidden("repository is not selected for this project")
	}
	return err
}
