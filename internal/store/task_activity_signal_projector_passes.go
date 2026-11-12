package store

import (
	"context"
	"errors"
	"time"
)

// Seed and tail passes of the neglect-signal projector. Classification reads
// through s.DB first; rows (and, for the tail, the cursor) are then written in
// one short BEGIN IMMEDIATE transaction.

const taskActivitySignalMissingRow = `NOT EXISTS (SELECT 1 FROM task_activity_signal s WHERE s.task_id=t.id AND s.classification_version=?)`

// ensureTaskActivitySignalProjection records the current MAX(events.cursor)
// as the processed cursor the first time any projector runs, before any seed
// rebuild. One statement, so it is atomic; an existing row is kept.
func (s *Store) ensureTaskActivitySignalProjection(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO task_activity_signal_projection(id, event_cursor, seeded_version, updated_at)
		SELECT 1, COALESCE(MAX(cursor), 0), 0, ? FROM events`, now())
	return err
}

// seedBatch rebuilds the next id-keyset batch of tasks lacking a current row,
// or marks the seed complete when none remain.
func (p *TaskActivitySignalProjector) seedBatch(ctx context.Context) error {
	db := p.store.DB
	if !p.seedStarted {
		var remaining int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks t WHERE `+taskActivitySignalMissingRow, TaskActivityClassificationVersion).Scan(&remaining); err != nil {
			return err
		}
		p.mu.Lock()
		p.stats.SeedRemaining = remaining
		p.mu.Unlock()
		p.seedStarted = true
	}
	rows, err := db.QueryContext(ctx, `SELECT t.id FROM tasks t WHERE t.id > ? AND `+taskActivitySignalMissingRow+` ORDER BY t.id LIMIT ?`,
		p.seedAfter, TaskActivityClassificationVersion, p.options.SeedBatch)
	if err != nil {
		return err
	}
	ids, err := scanTaskActivitySignalIDs(rows)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		if _, err := db.ExecContext(ctx, `UPDATE task_activity_signal_projection SET seeded_version=?, updated_at=? WHERE id=1`, TaskActivityClassificationVersion, now()); err != nil {
			return err
		}
		p.seedDone = true
		p.mu.Lock()
		p.stats.SeedComplete, p.stats.SeedRemaining = true, 0
		p.mu.Unlock()
		return nil
	}
	if err := p.rebuild(ctx, "seed", ids, nil); err != nil {
		return err
	}
	p.seedAfter = ids[len(ids)-1]
	return nil
}

// tailWindow rebuilds every task with an event in (processed, end], where end
// is at most one window past processed and never past high, then moves the
// cursor to end with the rows.
func (p *TaskActivitySignalProjector) tailWindow(ctx context.Context, processed, high int64) error {
	end := min(processed+p.options.TailWindow, high)
	rows, err := p.store.DB.QueryContext(ctx, `SELECT DISTINCT task_id FROM events WHERE cursor > ? AND cursor <= ? AND task_id IS NOT NULL`, processed, end)
	if err != nil {
		return err
	}
	ids, err := scanTaskActivitySignalIDs(rows)
	if err != nil {
		return err
	}
	if err := p.rebuild(ctx, "tail", ids, &end); err != nil {
		return err
	}
	p.observe(end)
	return nil
}

func scanTaskActivitySignalIDs(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]string, error) {
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// rebuild classifies ids, then writes their rows (and the tail cursor) in one
// transaction. A task deleted meanwhile is skipped; nothing is written if the
// pass fails.
func (p *TaskActivitySignalProjector) rebuild(ctx context.Context, pass string, ids []string, cursor *int64) error {
	signals := make([]TaskActivitySignal, 0, len(ids))
	var elapsed time.Duration
	errorsFound := uint64(0)
	for _, id := range ids {
		started := time.Now()
		signal, err := p.classifyOne(ctx, id)
		elapsed += time.Since(started)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if signal.Reason == TaskActivityReasonClassificationError {
			errorsFound++
		}
		signals = append(signals, signal)
	}
	if p.beforeWrite != nil {
		p.beforeWrite(pass, ids)
	}
	var held time.Duration
	written := int64(0)
	err := p.store.immediateTx(ctx, func(q dependencySQL) error {
		started := time.Now()
		defer func() { held = time.Since(started) }()
		computedAt := time.Now().UTC()
		for _, signal := range signals {
			ok, err := upsertTaskActivitySignal(ctx, q, signal, computedAt)
			if err != nil {
				return err
			}
			if ok {
				written++
			}
		}
		if cursor == nil {
			return nil
		}
		_, err := q.ExecContext(ctx, `UPDATE task_activity_signal_projection SET event_cursor=MAX(event_cursor, ?), updated_at=? WHERE id=1`, *cursor, now())
		return err
	})
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if pass == "seed" {
		p.stats.SeedRebuilds += uint64(len(signals))
		p.stats.SeedRebuildSeconds += elapsed.Seconds()
		p.stats.SeedRemaining = max(p.stats.SeedRemaining-written, 0)
	} else {
		p.stats.TailRebuilds += uint64(len(signals))
		p.stats.TailRebuildSeconds += elapsed.Seconds()
	}
	p.stats.Writes++
	p.stats.WriteSeconds += held.Seconds()
	p.stats.WriteMaxSeconds = max(p.stats.WriteMaxSeconds, held.Seconds())
	p.stats.ClassificationErrors += errorsFound
	return nil
}

// classifyOne classifies one task. A failure that is not the caller's
// cancellation or an unreachable database is retried once and then recorded
// as poison: unknown with classification_error, so it never blocks the
// cursor (plan D4).
func (p *TaskActivitySignalProjector) classifyOne(ctx context.Context, id string) (TaskActivitySignal, error) {
	classify := p.classify
	if classify == nil {
		classify = p.store.ClassifyTaskActivitySignal
	}
	signal, err := classify(ctx, id)
	if err == nil || errors.Is(err, ErrNotFound) {
		return signal, err
	}
	if ctx.Err() != nil {
		return TaskActivitySignal{}, ctx.Err()
	}
	if signal, err = classify(ctx, id); err == nil || errors.Is(err, ErrNotFound) {
		return signal, err
	}
	if ctx.Err() != nil {
		return TaskActivitySignal{}, ctx.Err()
	}
	if pingErr := p.store.DB.PingContext(ctx); pingErr != nil {
		return TaskActivitySignal{}, pingErr
	}
	poison := unknownTaskActivity(TaskActivityReasonClassificationError)
	poison.TaskID = id
	return poison, nil
}
