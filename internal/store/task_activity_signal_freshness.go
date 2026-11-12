package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Freshness reads for the neglect signal (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md
// D5). They only read: a request notes the newest event cursor, waits a short
// budget for the projector to reach it, and reports tasks whose rows are not
// yet current as pending. None of them creates the projection row or writes
// any derived row, so they are safe on any read path.

const (
	taskActivitySignalWaitPoll = 25 * time.Millisecond
	// taskActivitySignalIDChunk keeps id lists well under SQLite's bound
	// parameter limit.
	taskActivitySignalIDChunk = 400
)

// TaskActivitySignalProgress is the projector's position.
type TaskActivitySignalProgress struct {
	// Processed is the processed event cursor; 0 before any projector ran.
	Processed int64
	// High is MAX(events.cursor) in the same snapshot.
	High int64
	// SeededVersion is the classification version of the last completed
	// seed; below TaskActivityClassificationVersion some tasks may lack a
	// current row (TaskActivitySignalUnprocessed reports them).
	SeededVersion int
	// Initialized is false until a projector has recorded its first cursor.
	Initialized bool
}

// Lag is the number of event cursors not yet processed.
func (p TaskActivitySignalProgress) Lag() int64 { return max(p.High-p.Processed, 0) }

// Reached reports whether every event up to cursor has been processed.
func (p TaskActivitySignalProgress) Reached(cursor int64) bool {
	return p.Initialized && p.Processed >= cursor
}

// TaskActivitySignalProgress reads the processed and newest event cursors in
// one statement: two primary-key lookups, cheap enough for every request.
func (s *Store) TaskActivitySignalProgress(ctx context.Context) (TaskActivitySignalProgress, error) {
	var processed, seeded sql.NullInt64
	var progress TaskActivitySignalProgress
	err := s.DB.QueryRowContext(ctx, `SELECT (SELECT COALESCE(MAX(cursor), 0) FROM events), p.event_cursor, p.seeded_version
		FROM (SELECT 1) LEFT JOIN task_activity_signal_projection p ON p.id=1`).Scan(&progress.High, &processed, &seeded)
	if err != nil {
		return TaskActivitySignalProgress{}, err
	}
	progress.Initialized = processed.Valid
	progress.Processed, progress.SeededVersion = processed.Int64, int(seeded.Int64)
	return progress, nil
}

// WaitForTaskActivitySignal waits at most budget for the processed cursor to
// reach cursor (normally High noted at the start of a request). It wakes this
// process's projector once and then waits for it to advance, polling the
// database too so a projector in another process counts. It returns the last
// progress read and whether cursor was reached; running out of budget is not
// an error. A canceled ctx returns ctx.Err().
func (s *Store) WaitForTaskActivitySignal(ctx context.Context, cursor int64, budget time.Duration) (TaskActivitySignalProgress, bool, error) {
	progress, err := s.TaskActivitySignalProgress(ctx)
	if err != nil {
		return progress, false, err
	}
	if progress.Reached(cursor) || budget <= 0 {
		return progress, progress.Reached(cursor), nil
	}
	projector := s.activitySignal.Load()
	if projector != nil {
		projector.Wake()
	}
	deadline := time.NewTimer(budget)
	defer deadline.Stop()
	poll := time.NewTicker(taskActivitySignalWaitPoll)
	defer poll.Stop()
	for {
		var advanced <-chan struct{}
		if projector != nil {
			advanced = projector.advancedSignal()
		}
		expired := false
		select {
		case <-ctx.Done():
			return progress, false, ctx.Err()
		case <-deadline.C:
			expired = true
		case <-advanced:
		case <-poll.C:
		}
		if progress, err = s.TaskActivitySignalProgress(ctx); err != nil {
			return progress, false, err
		}
		if progress.Reached(cursor) || expired {
			return progress, progress.Reached(cursor), nil
		}
	}
}

// TaskActivitySignalUnprocessed returns which of taskIDs do not yet have a
// current row: those with an event past processed, or with no row at the
// current classification version (not yet seeded or reclassified). Readers
// return these as pending (plan D5). Ids of tasks that do not exist are
// omitted. Each lookup uses the task's event index; ids are queried in
// bounded chunks.
func (s *Store) TaskActivitySignalUnprocessed(ctx context.Context, processed int64, taskIDs []string) (map[string]bool, error) {
	result := make(map[string]bool)
	for start := 0; start < len(taskIDs); start += taskActivitySignalIDChunk {
		chunk := taskIDs[start:min(start+taskActivitySignalIDChunk, len(taskIDs))]
		args := make([]any, 0, len(chunk)+2)
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, TaskActivityClassificationVersion, processed)
		rows, err := s.DB.QueryContext(ctx, `SELECT t.id FROM tasks t WHERE t.id IN (?`+strings.Repeat(",?", len(chunk)-1)+`)
			AND (`+taskActivitySignalMissingRow+` OR EXISTS (SELECT 1 FROM events e WHERE e.task_id=t.id AND e.cursor > ?))`, args...)
		if err != nil {
			return nil, err
		}
		ids, err := scanTaskActivitySignalIDs(rows)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			result[id] = true
		}
	}
	return result, nil
}
