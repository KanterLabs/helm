package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) GetTask(ctx context.Context, id string) (Task, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks t WHERE t.id = ? AND t.deleted_at IS NULL`, id)
	task, err := taskFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, notFound("task not found")
	}
	if err != nil {
		return Task{}, err
	}
	if err := s.enrichTask(ctx, &task); err != nil {
		return Task{}, err
	}
	if err := s.populateDependencySummary(ctx, &task); err != nil {
		return Task{}, err
	}
	if err := s.populateTaskReleaseReference(ctx, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

// GetTaskForRestore returns the current task snapshot and whether it is soft
// deleted. Restore is intentionally the only caller that can inspect a
// deleted task; normal reads continue to hide deleted work.
func (s *Store) GetTaskForRestore(ctx context.Context, reference string) (Task, bool, error) {
	reference = strings.TrimSpace(reference)
	row := s.DB.QueryRowContext(ctx, `SELECT `+taskColumns+`, t.deleted_at
		FROM tasks t
		JOIN projects p ON p.id=t.project_id
		WHERE t.id=? OR lower(p.key || '-' || CAST(t.number AS TEXT))=lower(?)
		LIMIT 1`, reference, reference)
	var task Task
	var deletedAt sql.NullString
	var assignee, claimed, claimExpiry, due, completed, parent, release sql.NullString
	if err := row.Scan(&task.ID, &task.Number, &task.ProjectID, &task.Kind, &task.ColumnID, &task.Title, &task.Description, &task.Priority, &task.Position, &assignee, &claimed, &claimExpiry, &due, &task.Version, &completed, &task.CreatedAt, &task.UpdatedAt, &parent, &release, &deletedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, false, notFound("task not found")
		}
		return Task{}, false, err
	}
	task.Assignee, task.ClaimedBy, task.ClaimExpiresAt, task.DueAt, task.CompletedAt, task.ParentTaskID, task.ReleaseID = nullableString(assignee), nullableString(claimed), nullableString(claimExpiry), nullableString(due), nullableString(completed), nullableString(parent), nullableString(release)
	task.ParentID = task.ParentTaskID
	if err := s.enrichTask(ctx, &task); err != nil {
		return Task{}, false, err
	}
	if err := s.populateDependencySummary(ctx, &task); err != nil {
		return Task{}, false, err
	}
	if err := s.populateTaskReleaseReference(ctx, &task); err != nil {
		return Task{}, false, err
	}
	return task, deletedAt.Valid, nil
}

func txExecTaskCreate(ctx context.Context, tx *sql.Tx, id, projectID string, number int, columnID, kind, title, description, priority string, position float64, assignee, dueAt string, completedAt any, created, parentID, releaseID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO tasks(id, project_id, number, column_id, kind, title, description, priority, position, assignee_id, due_at, version, completed_at, created_at, updated_at, parent_task_id, release_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), 1, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))`, id, projectID, number, columnID, kind, title, description, priority, position, assignee, dueAt, completedAt, created, created, parentID, releaseID)
	return err
}

// ResolveTaskReference accepts either an opaque task ID or the project-local
// task key (for example, OPS-42), and always returns the fully enriched task.
// Project keys are compared case-insensitively while opaque IDs retain their
// exact-match semantics.
func (s *Store) ResolveTaskReference(ctx context.Context, reference string) (Task, error) {
	reference = strings.TrimSpace(reference)
	row := s.DB.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.deleted_at IS NULL AND (t.id=? OR lower(p.key || '-' || CAST(t.number AS TEXT))=lower(?)) LIMIT 1`, reference, reference)
	task, err := taskFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		// A ticket filed into a project keeps answering to its old key.
		if id, ok, aliasErr := s.resolveTaskAlias(ctx, reference); aliasErr != nil || ok {
			if aliasErr != nil {
				return Task{}, aliasErr
			}
			return s.GetTask(ctx, id)
		}
		return Task{}, notFound("task not found")
	}
	if err != nil {
		return Task{}, err
	}
	if err := s.enrichTask(ctx, &task); err != nil {
		return Task{}, err
	}
	if err := s.populateDependencySummary(ctx, &task); err != nil {
		return Task{}, err
	}
	if err := s.populateTaskReleaseReference(ctx, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *Store) enrichTask(ctx context.Context, task *Task) error {
	return s.enrichTaskAt(ctx, task, time.Now().UTC())
}

func (s *Store) enrichTaskAt(ctx context.Context, task *Task, at time.Time) error {
	var key string
	if err := s.DB.QueryRowContext(ctx, `SELECT key FROM projects WHERE id = ?`, task.ProjectID).Scan(&key); err != nil {
		return err
	}
	task.Key = fmt.Sprintf("%s-%d", key, task.Number)
	rows, err := s.DB.QueryContext(ctx, `SELECT l.id, l.project_id, l.name, l.color, l.created_at, l.updated_at FROM labels l JOIN task_labels tl ON tl.label_id=l.id WHERE tl.task_id=? ORDER BY lower(l.name), l.id`, task.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		label, err := labelFromRow(rows)
		if err != nil {
			rows.Close()
			return err
		}
		task.Labels = append(task.Labels, label)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if task.Labels == nil {
		task.Labels = []Label{}
	}
	if task.Kind == bugKind {
		bug, err := s.getBugDetails(ctx, task.ID)
		if err != nil {
			return err
		}
		task.Bug = bug
	}
	if err := s.populateTaskChecklist(ctx, task); err != nil {
		return err
	}
	work, err := s.agentWorkAt(ctx, task.ID, at)
	if err != nil {
		return err
	}
	task.AgentWork = work
	if err := s.populateAlertSource(ctx, task); err != nil {
		return err
	}
	if err := s.populateTicket(ctx, task); err != nil {
		return err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM comments WHERE task_id=? AND deleted_at IS NULL`, task.ID).Scan(&task.CommentCount); err != nil {
		return err
	}
	return s.populateTaskHierarchyAt(ctx, task, at)
}
