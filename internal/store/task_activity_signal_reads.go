package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Task.activity on reads (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md D2, D5; wire
// contract in test/neglect/API.md). Reads never write and never wait here:
// a task whose events the projector has not processed, or that has no row at
// the current classification version, is reported as transport-only
// "pending" with no evidence fields.

const (
	// TaskActivityPending is a transport status; it is never stored.
	TaskActivityPending = "pending"
	// TaskActivityUndetermined is a listing filter value, not a status: a
	// bounded row newer than the inactivity threshold.
	TaskActivityUndetermined = "undetermined"
)

// TaskActivity is the public Task.activity object. Times are RFC 3339 UTC.
type TaskActivity struct {
	Status                string  `json:"status"`
	LastMeaningfulAt      *string `json:"last_meaningful_at"`
	Source                string  `json:"source"`
	NotAfter              *string `json:"not_after"`
	Reason                string  `json:"reason"`
	ClassificationVersion *int    `json:"classification_version"`
	// sortNotAfter is the stored not_after text the not_after keyset order
	// compared (empty when the task has no row or no bound).
	sortNotAfter string
}

type taskActivitySignalStored struct {
	status, source, reason string
	lastMeaningfulAt       sql.NullString
	notAfter               sql.NullString
	version                int
}

// PopulateTaskActivity sets Activity on every task with one progress read,
// one batched row read and, only while the projector is behind, one batched
// unprocessed lookup. It never waits for the projector.
func (s *Store) PopulateTaskActivity(ctx context.Context, tasks []Task) error {
	s.observe("task_activity_signal_progress", nil)
	progress, err := s.TaskActivitySignalProgress(ctx)
	if err != nil {
		return err
	}
	_, err = s.populateTaskActivityAt(ctx, tasks, progress, progress.High)
	return err
}

// populateTaskActivityAt marks tasks pending relative to progress: when the
// processed cursor is below observedHigh, a task with an event past it is
// pending. It returns the number of pending tasks.
func (s *Store) populateTaskActivityAt(ctx context.Context, tasks []Task, progress TaskActivitySignalProgress, observedHigh int64) (int, error) {
	ids := make([]string, 0, len(tasks))
	seen := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		if task.ID != "" && !seen[task.ID] {
			seen[task.ID] = true
			ids = append(ids, task.ID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	stored, err := s.readTaskActivitySignalRows(ctx, ids)
	if err != nil {
		return 0, err
	}
	unprocessed := map[string]bool{}
	if !progress.Initialized || progress.Processed < observedHigh {
		s.observe("task_activity_signal_unprocessed", nil)
		if unprocessed, err = s.TaskActivitySignalUnprocessed(ctx, progress.Processed, ids); err != nil {
			return 0, err
		}
	}
	pending := 0
	for index := range tasks {
		row, ok := stored[tasks[index].ID]
		activity := taskActivityFromStored(row, ok)
		if !ok || row.version != TaskActivityClassificationVersion || unprocessed[tasks[index].ID] {
			activity = TaskActivity{Status: TaskActivityPending, sortNotAfter: activity.sortNotAfter}
			pending++
		}
		tasks[index].Activity = &activity
	}
	return pending, nil
}

func (s *Store) readTaskActivitySignalRows(ctx context.Context, ids []string) (map[string]taskActivitySignalStored, error) {
	result := make(map[string]taskActivitySignalStored, len(ids))
	for start := 0; start < len(ids); start += taskActivitySignalIDChunk {
		chunk := ids[start:min(start+taskActivitySignalIDChunk, len(ids))]
		args := make([]any, len(chunk))
		for index, id := range chunk {
			args[index] = id
		}
		query := `SELECT task_id, status, last_meaningful_at, source, not_after, reason, classification_version
			FROM task_activity_signal WHERE task_id IN (?` + strings.Repeat(",?", len(chunk)-1) + `)`
		s.observe(query, args)
		rows, err := s.DB.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var row taskActivitySignalStored
			if err := rows.Scan(&id, &row.status, &row.lastMeaningfulAt, &row.source, &row.notAfter, &row.reason, &row.version); err != nil {
				rows.Close()
				return nil, err
			}
			result[id] = row
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func taskActivityFromStored(row taskActivitySignalStored, ok bool) TaskActivity {
	if !ok {
		return TaskActivity{Status: TaskActivityPending}
	}
	version := row.version
	activity := TaskActivity{Status: row.status, Source: row.source, Reason: row.reason, ClassificationVersion: &version,
		LastMeaningfulAt: publicTaskActivityTime(row.lastMeaningfulAt), NotAfter: publicTaskActivityTime(row.notAfter)}
	if row.notAfter.Valid {
		activity.sortNotAfter = row.notAfter.String
	}
	return activity
}

// publicTaskActivityTime renders a stored fixed-width instant as RFC 3339
// with trailing zero fractions trimmed, like every other API timestamp. An
// unparseable stored value (never written by the projector) is passed on.
func publicTaskActivityTime(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		text := value.String
		return &text
	}
	text := parsed.UTC().Format(time.RFC3339Nano)
	return &text
}
