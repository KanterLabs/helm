package store

import (
	"context"
	"strings"
	"time"
)

func (s *Store) ListMyWork(ctx context.Context, actorID string, limit int) ([]Task, bool, error) {
	return s.ListMyWorkFiltered(ctx, actorID, nil, TaskFilter{Limit: limit})
}

func (s *Store) ListMyWorkScoped(ctx context.Context, actorID string, projectIDs []string, limit int) ([]Task, bool, error) {
	return s.ListMyWorkFiltered(ctx, actorID, projectIDs, TaskFilter{Limit: limit})
}

func (s *Store) ListMyWorkFiltered(ctx context.Context, actorID string, projectIDs []string, filter TaskFilter) ([]Task, bool, error) {
	return s.listMyWorkFiltered(ctx, actorID, projectIDs, filter, false)
}

// ListMyWorkFilteredWithExtra fetches one sentinel row for cursor endpoints;
// ordinary callers retain the public 200-row cap.
func (s *Store) ListMyWorkFilteredWithExtra(ctx context.Context, actorID string, projectIDs []string, filter TaskFilter) ([]Task, bool, error) {
	return s.listMyWorkFiltered(ctx, actorID, projectIDs, filter, true)
}

func (s *Store) listMyWorkFiltered(ctx context.Context, actorID string, projectIDs []string, filter TaskFilter, allowExtra bool) ([]Task, bool, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	maxLimit := 200
	if allowExtra {
		maxLimit++
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	readAt := time.Now().UTC()
	timestamp := readAt.Format(time.RFC3339Nano)
	query := `SELECT ` + taskColumns + ` FROM tasks t JOIN columns c ON c.id=t.column_id`
	args := []any{}
	if filter.LiveWork {
		// Live work is a workspace pulse view rather than an actor-assignment
		// view: every unfinished task with a pulse in an active project is
		// eligible, subject to the caller's project allow-list.
		query += ` JOIN task_agent_work aw ON aw.task_id=t.id JOIN projects p ON p.id=t.project_id WHERE t.deleted_at IS NULL AND t.completed_at IS NULL AND p.archived_at IS NULL`
	} else {
		query += ` WHERE t.deleted_at IS NULL AND (t.assignee_id=? OR (t.claimed_by=? AND t.claim_expires_at IS NOT NULL AND julianday(t.claim_expires_at) > julianday(?)))`
		args = append(args, actorID, actorID, timestamp)
	}
	staleCutoff := agentWorkStaleCutoff(readAt)
	if projectIDs != nil && len(projectIDs) == 0 {
		query += ` AND 1=0`
	}
	if len(projectIDs) > 0 {
		placeholders := make([]string, len(projectIDs))
		for i, projectID := range projectIDs {
			placeholders[i] = "?"
			args = append(args, projectID)
		}
		query += ` AND t.project_id IN (` + strings.Join(placeholders, ",") + ")"
	}
	releaseFilter := filter.ReleaseID
	if releaseFilter == "" {
		releaseFilter = filter.Release
	}
	if releaseFilter != "" {
		if strings.EqualFold(strings.TrimSpace(releaseFilter), "unassigned") || strings.EqualFold(strings.TrimSpace(releaseFilter), "none") {
			query += ` AND t.release_id IS NULL`
		} else {
			query += ` AND t.release_id=?`
			args = append(args, strings.TrimSpace(releaseFilter))
		}
	}
	if filter.State != "" {
		query += ` AND c.semantic_state=?`
		args = append(args, filter.State)
	}
	if filter.Dependency != "" {
		switch strings.ToLower(strings.TrimSpace(filter.Dependency)) {
		case "blocked":
			query += ` AND EXISTS (
				SELECT 1
				FROM task_dependencies td
				JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
				JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
				WHERE td.task_id=t.id
				  AND prerequisite.project_id=t.project_id
				  AND prerequisite.deleted_at IS NULL
				  AND (prerequisite.completed_at IS NULL OR prerequisite_column.semantic_state <> 'completed'))`
		case "ready":
			query += ` AND EXISTS (
				SELECT 1
				FROM task_dependencies td
				JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
				WHERE td.task_id=t.id
				  AND prerequisite.project_id=t.project_id
				  AND prerequisite.deleted_at IS NULL)
			AND NOT EXISTS (
				SELECT 1
				FROM task_dependencies td
				JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
				JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
				WHERE td.task_id=t.id
				  AND prerequisite.project_id=t.project_id
				  AND prerequisite.deleted_at IS NULL
				  AND (prerequisite.completed_at IS NULL OR prerequisite_column.semantic_state <> 'completed'))`
		default:
			return nil, false, invalid("dependency must be blocked or ready", map[string]any{"dependency": filter.Dependency})
		}
	}
	// Assigned work may still show a completed task when unfiltered, preserving
	// the legacy assignment view. Liveness filters only classify unfinished
	// tasks, so a completed task with a retained snapshot must be excluded.
	if !filter.LiveWork && (filter.AgentState != "" || filter.ActionNeeded) {
		query += ` AND t.completed_at IS NULL`
	}
	if filter.AgentState != "" {
		switch filter.AgentState {
		case "missing":
			if filter.LiveWork {
				// The live view's inner join already excludes missing pulses.
				query += ` AND 1=0`
			} else {
				query += ` AND NOT EXISTS (SELECT 1 FROM task_agent_work aw WHERE aw.task_id=t.id)`
			}
		case "stale":
			if filter.LiveWork {
				query += ` AND julianday(aw.updated_at) <= julianday(?)`
			} else {
				query += ` AND EXISTS (SELECT 1 FROM task_agent_work aw WHERE aw.task_id=t.id AND julianday(aw.updated_at) <= julianday(?))`
			}
			args = append(args, staleCutoff)
		default:
			if filter.LiveWork {
				query += ` AND aw.state=?`
			} else {
				query += ` AND EXISTS (SELECT 1 FROM task_agent_work aw WHERE aw.task_id=t.id AND aw.state=?)`
			}
			args = append(args, filter.AgentState)
		}
	}
	if filter.ActionNeeded {
		if filter.LiveWork {
			query += ` AND (aw.state IN ('waiting', 'handoff') OR julianday(aw.updated_at) <= julianday(?))`
		} else {
			query += ` AND EXISTS (SELECT 1 FROM task_agent_work aw WHERE aw.task_id=t.id AND (aw.state IN ('waiting', 'handoff') OR julianday(aw.updated_at) <= julianday(?)))`
		}
		args = append(args, staleCutoff)
	}
	if filter.Priority != "" {
		query += ` AND t.priority=?`
		args = append(args, filter.Priority)
	}
	if filter.Label != "" {
		query += ` AND EXISTS (SELECT 1 FROM task_labels tl JOIN labels l ON l.id=tl.label_id WHERE tl.task_id=t.id AND (l.id=? OR lower(l.name)=lower(?)))`
		args = append(args, filter.Label, filter.Label)
	}
	if filter.Query != "" {
		query += ` AND (lower(t.title) LIKE ? OR lower(t.description) LIKE ?)`
		value := "%" + strings.ToLower(filter.Query) + "%"
		args = append(args, value, value)
	}
	if filter.UpdatedAfter != nil {
		query += ` AND t.updated_at > ?`
		args = append(args, filter.UpdatedAfter.UTC().Format(time.RFC3339Nano))
	}
	// Cursor is an opaque offset. Live work prioritizes actionable pulses and
	// then the newest pulse; the legacy view retains its claimed/board order.
	if filter.LiveWork {
		query += ` ORDER BY CASE WHEN aw.state IN ('waiting', 'handoff') OR julianday(aw.updated_at) <= julianday(?) THEN 0 ELSE 1 END, aw.updated_at DESC, t.updated_at DESC, t.id LIMIT ? OFFSET ?`
		args = append(args, staleCutoff, limit+1, filter.Cursor)
	} else {
		query += ` ORDER BY CASE WHEN t.claimed_by=? AND t.claim_expires_at IS NOT NULL AND julianday(t.claim_expires_at) > julianday(?) THEN 0 ELSE 1 END, c.position, t.position, t.updated_at DESC, t.id LIMIT ? OFFSET ?`
		args = append(args, actorID, timestamp, limit+1, filter.Cursor)
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]Task, 0, limit)
	for rows.Next() {
		task, err := taskFromRow(rows)
		if err != nil {
			return nil, false, err
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	for i := range result {
		if err := s.enrichTaskAt(ctx, &result[i], readAt); err != nil {
			return nil, false, err
		}
	}
	if err := s.populateTaskDependencySummaries(ctx, result); err != nil {
		return nil, false, err
	}
	if err := s.populateTaskReleaseReferences(ctx, result); err != nil {
		return nil, false, err
	}
	return result, hasMore, nil
}

func (s *Store) SeedDemo(ctx context.Context, actorID string) error {
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM projects`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	name, key := "Demo roadmap", "DEMO"
	_, err := s.CreateProject(ctx, ProjectInput{Key: &key, Name: &name}, actorID)
	if err != nil {
		return err
	}
	return nil
}
