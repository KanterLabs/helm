package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) CreateWatch(ctx context.Context, actorID, projectReference, taskReference string) (Watch, error) {
	if strings.TrimSpace(actorID) == "" {
		return Watch{}, invalid("actor is required", nil)
	}
	if _, err := s.GetActor(ctx, actorID); err != nil {
		return Watch{}, err
	}
	project, err := s.GetProject(ctx, projectReference)
	if err != nil {
		return Watch{}, err
	}
	taskID := ""
	if strings.TrimSpace(taskReference) != "" {
		task, err := s.ResolveTaskReference(ctx, taskReference)
		if err != nil {
			return Watch{}, err
		}
		if task.ProjectID != project.ID {
			return Watch{}, invalid("task belongs to another project", nil)
		}
		taskID = task.ID
	}
	id, created := newID(), now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO watches(id, actor_id, project_id, task_id, created_at) VALUES (?, ?, ?, NULLIF(?, ''), ?)`, id, actorID, project.ID, taskID, created); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return &Error{Kind: ErrAlreadyExists, Message: "watch already exists"}
			}
			if strings.Contains(strings.ToLower(err.Error()), "watch_task_project_mismatch") {
				return invalid("task belongs to another project", nil)
			}
			return err
		}
		watchType := "project"
		if taskID != "" {
			watchType = "task"
		}
		_, err := insertEvent(ctx, tx, "watch.created", actorID, project.ID, taskID, map[string]any{"watch_id": id, "watch_type": watchType})
		return err
	})
	if err != nil {
		return Watch{}, err
	}
	return s.GetWatch(ctx, id, actorID)
}

func (s *Store) CreateProjectWatch(ctx context.Context, actorID, projectReference string) (Watch, error) {
	return s.CreateWatch(ctx, actorID, projectReference, "")
}

func (s *Store) CreateTaskWatch(ctx context.Context, actorID, taskReference string) (Watch, error) {
	task, err := s.ResolveTaskReference(ctx, taskReference)
	if err != nil {
		return Watch{}, err
	}
	return s.CreateWatch(ctx, actorID, task.ProjectID, task.ID)
}

func (s *Store) GetWatch(ctx context.Context, id, actorID string) (Watch, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, actor_id, project_id, task_id, created_at FROM watches WHERE id=?`, id)
	watch, err := watchFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Watch{}, notFound("watch not found")
	}
	if err != nil {
		return Watch{}, err
	}
	if actorID != "" && watch.ActorID != actorID {
		return Watch{}, forbidden("watch belongs to another actor")
	}
	return watch, nil
}

// appendNotificationVisibility applies both the durable actor project ceiling
// and an optional bearer-token allow-list. A nil projectIDs value means the
// caller is unscoped; an empty, non-nil value intentionally matches no rows.
func appendNotificationVisibility(query string, args []any, actorID string, projectIDs []string, projectColumn string) (string, []any) {
	query += ` AND EXISTS (
  SELECT 1 FROM actors a WHERE a.id=? AND a.disabled_at IS NULL AND (
   (a.kind='agent' AND (
    NOT EXISTS (SELECT 1 FROM actor_projects ceiling WHERE ceiling.actor_id=a.id)
    OR EXISTS (SELECT 1 FROM actor_projects allowed WHERE allowed.actor_id=a.id AND allowed.project_id=` + projectColumn + `)))
   OR (a.kind='human' AND EXISTS (
    SELECT 1 FROM workspace_memberships wm WHERE wm.actor_id=a.id AND (
     wm.role IN ('owner','admin') OR EXISTS (
      SELECT 1 FROM project_memberships pm WHERE pm.actor_id=a.id AND pm.project_id=` + projectColumn + `))))
  )
 )`
	args = append(args, actorID)
	if projectIDs == nil {
		return query, args
	}
	if len(projectIDs) == 0 {
		return query + ` AND 1=0`, args
	}
	placeholders := make([]string, len(projectIDs))
	for i, projectID := range projectIDs {
		placeholders[i] = `?`
		args = append(args, projectID)
	}
	query += ` AND ` + projectColumn + ` IN (` + strings.Join(placeholders, `,`) + `)`
	return query, args
}

// ListWatches returns watches owned by actorID. A project or task filter may
// be supplied by callers that need to render current watch state. The legacy
// unscoped entry point still enforces the actor's durable project ceiling.
func (s *Store) ListWatches(ctx context.Context, actorID, projectID, taskID string) ([]Watch, error) {
	return s.ListWatchesScoped(ctx, actorID, projectID, taskID, nil)
}

// ListWatchesScoped applies the effective project allow-list of a bearer
// token in addition to the actor's durable project ceiling.
func (s *Store) ListWatchesScoped(ctx context.Context, actorID, projectID, taskID string, projectIDs []string) ([]Watch, error) {
	query := `SELECT w.id, w.actor_id, w.project_id, w.task_id, w.created_at FROM watches w WHERE w.actor_id=?`
	args := []any{actorID}
	query, args = appendNotificationVisibility(query, args, actorID, projectIDs, "w.project_id")
	if projectID != "" {
		query += ` AND w.project_id=?`
		args = append(args, projectID)
	}
	if taskID != "" {
		query += ` AND (w.task_id IS NULL OR w.task_id=?)`
		args = append(args, taskID)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Watch, 0)
	for rows.Next() {
		watch, err := watchFromRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, watch)
	}
	return result, rows.Err()
}

func (s *Store) DeleteWatch(ctx context.Context, actorID, id string) error {
	var watch Watch
	row := s.DB.QueryRowContext(ctx, `SELECT id, actor_id, project_id, task_id, created_at FROM watches WHERE id=?`, id)
	var err error
	watch, err = watchFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound("watch not found")
	}
	if err != nil {
		return err
	}
	if watch.ActorID != actorID {
		return forbidden("watch belongs to another actor")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM watches WHERE id=? AND actor_id=?`, id, actorID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			return notFound("watch not found")
		}
		_, err = insertEvent(ctx, tx, "watch.deleted", actorID, watch.ProjectID, notificationStringValue(watch.TaskID), map[string]any{"watch_id": id})
		return err
	})
}
