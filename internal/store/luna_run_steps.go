package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const maxLunaRunSteps = 32

var lunaRunStepKinds = map[string]struct{}{
	"thread_started":     {},
	"turn_started":       {},
	"response_generated": {},
	"validation":         {},
	"outcome":            {},
}

// LunaRunStep is the intentionally small, prompt-free timeline exposed with
// a Luna run. The store never accepts or returns protocol parameters,
// generated text, credentials, or arbitrary diagnostics as a step.
type LunaRunStep struct {
	Sequence int    `json:"sequence"`
	Kind     string `json:"kind"`
	At       string `json:"at"`
}

type LunaRunStepInput struct {
	Kind string
}

func (s *Store) AppendLunaRunStep(ctx context.Context, runID string, input LunaRunStepInput) error {
	runID = strings.TrimSpace(runID)
	kind := strings.TrimSpace(input.Kind)
	if runID == "" {
		return invalid("luna run id is required", nil)
	}
	if _, ok := lunaRunStepKinds[kind]; !ok {
		return invalid("unsupported Luna run step", nil)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var outcome string
		if err := tx.QueryRowContext(ctx, `SELECT outcome FROM luna_runs WHERE id=?`, runID).Scan(&outcome); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if outcome != "running" {
			return ErrConflict
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM luna_run_steps WHERE run_id=?`, runID).Scan(&count); err != nil {
			return err
		}
		if count >= maxLunaRunSteps {
			// Dropping a later milestone preserves the bounded ledger and keeps
			// timeline persistence from changing the model run's outcome.
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO luna_run_steps(id,run_id,sequence,kind,at) VALUES (?,?,?,?,?)`, newID(), runID, count+1, kind, now())
		return err
	})
}

func (s *Store) populateLunaRunSteps(ctx context.Context, actorID string, runs []LunaRun) error {
	if len(runs) == 0 {
		return nil
	}
	byID := make(map[string]*LunaRun, len(runs))
	for index := range runs {
		runs[index].Steps = make([]LunaRunStep, 0)
		byID[runs[index].ID] = &runs[index]
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT s.run_id,s.sequence,s.kind,s.at FROM luna_run_steps s JOIN luna_runs r ON r.id=s.run_id WHERE r.actor_id=? ORDER BY s.run_id,s.sequence`, actorID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var runID string
		var step LunaRunStep
		if err := rows.Scan(&runID, &step.Sequence, &step.Kind, &step.At); err != nil {
			return err
		}
		if run := byID[runID]; run != nil {
			run.Steps = append(run.Steps, step)
		}
	}
	return rows.Err()
}
