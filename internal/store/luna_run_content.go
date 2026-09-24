package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	maxLunaRunInputBytes  = 64 << 10
	maxLunaRunOutputBytes = 2 << 20
)

// LunaRunContent is private inspection data for one initiating human. It is
// loaded only by GetLunaRunDetail; metadata list callers never receive it.
type LunaRunContent struct {
	InputText       string
	OutputText      string
	InputTruncated  bool
	OutputTruncated bool
}

func (s *Store) SaveLunaRunInput(ctx context.Context, runID, input string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return invalid("luna run id is required", nil)
	}
	input, truncated := truncateLunaRunContent(input, maxLunaRunInputBytes)
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireLunaRunContentParent(ctx, tx, runID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO luna_run_content(run_id,input_text,input_truncated,updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(run_id) DO UPDATE SET input_text=excluded.input_text,input_truncated=excluded.input_truncated,updated_at=excluded.updated_at`, runID, input, boolInt(truncated), now())
		return err
	})
}

func (s *Store) SaveLunaRunOutput(ctx context.Context, runID, output string, truncated bool) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return invalid("luna run id is required", nil)
	}
	output, storedTruncated := truncateLunaRunContent(output, maxLunaRunOutputBytes)
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireLunaRunContentParent(ctx, tx, runID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO luna_run_content(run_id,output_text,output_truncated,updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(run_id) DO UPDATE SET output_text=excluded.output_text,output_truncated=CASE WHEN excluded.output_truncated=1 OR luna_run_content.output_truncated=1 THEN 1 ELSE 0 END,updated_at=excluded.updated_at`, runID, output, boolInt(truncated || storedTruncated), now())
		return err
	})
}

func requireLunaRunContentParent(ctx context.Context, tx *sql.Tx, runID string) error {
	var found int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM luna_runs WHERE id=?`, runID).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func truncateLunaRunContent(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	cut := []byte(value[:limit])
	for len(cut) > 0 && !utf8.Valid(cut) {
		cut = cut[:len(cut)-1]
	}
	return string(cut), true
}

func (s *Store) GetLunaRunDetail(ctx context.Context, actorID, runID string) (LunaRun, LunaRunContent, bool, error) {
	var run LunaRun
	var projectID, completedAt sql.NullString
	var duration, outputBytes sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT id,actor_id,project_id,project_key,feature,outcome,model,effort,thread_id,turn_id,duration_ms,output_bytes,detail,started_at,completed_at FROM luna_runs WHERE actor_id=? AND id=?`, actorID, runID).Scan(
		&run.ID, &run.ActorID, &projectID, &run.ProjectKey, &run.Feature, &run.Outcome, &run.Model, &run.Effort, &run.ThreadID, &run.TurnID, &duration, &outputBytes, &run.Detail, &run.StartedAt, &completedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return LunaRun{}, LunaRunContent{}, false, ErrNotFound
	}
	if err != nil {
		return LunaRun{}, LunaRunContent{}, false, err
	}
	run.ProjectID, run.CompletedAt = nullableString(projectID), nullableString(completedAt)
	run.Steps = make([]LunaRunStep, 0)
	if duration.Valid {
		value := duration.Int64
		run.DurationMS = &value
	}
	if outputBytes.Valid {
		value := outputBytes.Int64
		run.OutputBytes = &value
	}
	runs := []LunaRun{run}
	if err := s.populateLunaRunSteps(ctx, actorID, runs); err != nil {
		return LunaRun{}, LunaRunContent{}, false, err
	}
	run = runs[0]

	var content LunaRunContent
	var inputTruncated, outputTruncated int
	err = s.DB.QueryRowContext(ctx, `SELECT input_text,output_text,input_truncated,output_truncated FROM luna_run_content WHERE run_id=?`, run.ID).Scan(&content.InputText, &content.OutputText, &inputTruncated, &outputTruncated)
	if errors.Is(err, sql.ErrNoRows) {
		return run, LunaRunContent{}, false, nil
	}
	if err != nil {
		return LunaRun{}, LunaRunContent{}, false, err
	}
	content.InputTruncated = boolValue(inputTruncated)
	content.OutputTruncated = boolValue(outputTruncated)
	return run, content, true, nil
}
