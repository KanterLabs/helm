package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ClassifyTaskActivitySignal reads one task's complete history through the
// timeline iterator and classifies it (plan D1 to D3). It reads only, so a
// projector can classify a batch before writing the rows and its cursor in
// one transaction. Soft-deleted and terminal tasks are classified like any
// other; a task that does not exist is not found.
func (s *Store) ClassifyTaskActivitySignal(ctx context.Context, taskID string) (TaskActivitySignal, error) {
	var createdAt sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT created_at FROM tasks WHERE id=?`, taskID).Scan(&createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskActivitySignal{}, notFound("task not found")
	}
	if err != nil {
		return TaskActivitySignal{}, err
	}
	var items []TaskTimelineItem
	undecodable := 0
	err = s.eachTaskTimelineItem(ctx, taskID, func(item TaskTimelineItem, decodeErr error) error {
		if decodeErr != nil {
			undecodable++
			return nil
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return TaskActivitySignal{}, err
	}
	signal := classifyTaskActivity(createdAt.String, items, undecodable)
	signal.TaskID = taskID
	return signal, nil
}

// upsertTaskActivitySignal writes one derived row idempotently. It runs on
// the store or inside a caller's transaction. A task deleted since it was
// classified writes nothing and reports false; ON DELETE CASCADE removes
// rows of tasks deleted afterwards.
func upsertTaskActivitySignal(ctx context.Context, exec dependencySQL, signal TaskActivitySignal, computedAt time.Time) (bool, error) {
	result, err := exec.ExecContext(ctx, `INSERT INTO task_activity_signal(task_id, status, last_meaningful_at, source, not_after, reason, classification_version, computed_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM tasks WHERE id=?)
		ON CONFLICT(task_id) DO UPDATE SET status=excluded.status, last_meaningful_at=excluded.last_meaningful_at,
			source=excluded.source, not_after=excluded.not_after, reason=excluded.reason,
			classification_version=excluded.classification_version, computed_at=excluded.computed_at`,
		signal.TaskID, signal.Status, formatTaskActivitySignalTime(signal.LastMeaningfulAt), signal.Source,
		formatTaskActivitySignalTime(signal.NotAfter), signal.Reason, signal.ClassificationVersion,
		computedAt.UTC().Format(taskActivitySignalTimeLayout), signal.TaskID)
	if err != nil {
		return false, err
	}
	written, err := result.RowsAffected()
	return written > 0, err
}

// RebuildTaskActivitySignal reclassifies one task from its complete history
// and upserts its task_activity_signal row. It is idempotent: rerunning it
// changes nothing but computed_at.
func (s *Store) RebuildTaskActivitySignal(ctx context.Context, taskID string) (TaskActivitySignal, error) {
	signal, err := s.ClassifyTaskActivitySignal(ctx, taskID)
	if err != nil {
		return TaskActivitySignal{}, err
	}
	computedAt := time.Now().UTC()
	written, err := upsertTaskActivitySignal(ctx, s.DB, signal, computedAt)
	if err != nil {
		return TaskActivitySignal{}, err
	}
	if !written {
		return TaskActivitySignal{}, notFound("task not found")
	}
	signal.ComputedAt = computedAt.Format(taskActivitySignalTimeLayout)
	return signal, nil
}
