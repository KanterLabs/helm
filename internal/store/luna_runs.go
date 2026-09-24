package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

const maxLunaRunsPerActor = 200

// LunaRun is a privacy-bounded execution record for one explicitly requested
// Luna model turn. It never contains prompts, source task text, model output,
// account metadata, or credentials.
type LunaRun struct {
	ID          string        `json:"id"`
	ActorID     string        `json:"-"`
	ProjectID   *string       `json:"project_id,omitempty"`
	ProjectKey  string        `json:"project_key,omitempty"`
	Feature     string        `json:"feature"`
	Outcome     string        `json:"outcome"`
	Model       string        `json:"model"`
	Effort      string        `json:"effort"`
	ThreadID    string        `json:"thread_id,omitempty"`
	TurnID      string        `json:"turn_id,omitempty"`
	DurationMS  *int64        `json:"duration_ms,omitempty"`
	OutputBytes *int64        `json:"output_bytes,omitempty"`
	Detail      string        `json:"detail,omitempty"`
	StartedAt   string        `json:"started_at"`
	CompletedAt *string       `json:"completed_at,omitempty"`
	Steps       []LunaRunStep `json:"steps"`
}

type LunaRunStart struct {
	ActorID    string
	ProjectID  string
	ProjectKey string
	Feature    string
	Model      string
	Effort     string
}

type LunaRunFinish struct {
	Outcome     string
	ThreadID    string
	TurnID      string
	DurationMS  int64
	OutputBytes int64
	Detail      string
}

func (s *Store) StartLunaRun(ctx context.Context, input LunaRunStart) (LunaRun, error) {
	started := now()
	run := LunaRun{
		ID: newID(), ActorID: strings.TrimSpace(input.ActorID), ProjectKey: strings.TrimSpace(input.ProjectKey),
		Feature: strings.TrimSpace(input.Feature), Outcome: "running", Model: strings.TrimSpace(input.Model),
		Effort: strings.TrimSpace(input.Effort), StartedAt: started,
	}
	if projectID := strings.TrimSpace(input.ProjectID); projectID != "" {
		run.ProjectID = &projectID
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO luna_runs(id,actor_id,project_id,project_key,feature,outcome,model,effort,started_at) VALUES (?,?,NULLIF(?,''),?,?,?,?,?,?)`,
			run.ID, run.ActorID, input.ProjectID, run.ProjectKey, run.Feature, run.Outcome, run.Model, run.Effort, run.StartedAt)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM luna_runs WHERE actor_id=? AND id NOT IN (SELECT id FROM luna_runs WHERE actor_id=? ORDER BY started_at DESC,id DESC LIMIT ?)`, run.ActorID, run.ActorID, maxLunaRunsPerActor)
		return err
	})
	return run, err
}

func (s *Store) FinishLunaRun(ctx context.Context, id string, input LunaRunFinish) error {
	duration := input.DurationMS
	if duration < 0 {
		duration = 0
	} else if duration > 300_000 {
		duration = 300_000
	}
	outputBytes := input.OutputBytes
	if outputBytes < 0 {
		outputBytes = 0
	} else if outputBytes > 2<<20 {
		outputBytes = 2 << 20
	}
	detail := strings.TrimSpace(input.Detail)
	detail = truncateLunaRunText(detail, 500)
	threadID := truncateLunaRunText(strings.TrimSpace(input.ThreadID), 200)
	turnID := truncateLunaRunText(strings.TrimSpace(input.TurnID), 200)
	_, err := s.DB.ExecContext(ctx, `UPDATE luna_runs SET outcome=?,thread_id=?,turn_id=?,duration_ms=?,output_bytes=?,detail=?,completed_at=? WHERE id=? AND outcome='running'`,
		strings.TrimSpace(input.Outcome), threadID, turnID, duration, outputBytes, detail, now(), id)
	return err
}

func truncateLunaRunText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func (s *Store) ListLunaRuns(ctx context.Context, actorID string, limit int) ([]LunaRun, error) {
	if limit < 1 {
		limit = 25
	} else if limit > 100 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,actor_id,project_id,project_key,feature,outcome,model,effort,thread_id,turn_id,duration_ms,output_bytes,detail,started_at,completed_at FROM luna_runs WHERE actor_id=? ORDER BY started_at DESC,id DESC LIMIT ?`, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]LunaRun, 0)
	for rows.Next() {
		var run LunaRun
		var projectID, completedAt sql.NullString
		var duration, outputBytes sql.NullInt64
		if err := rows.Scan(&run.ID, &run.ActorID, &projectID, &run.ProjectKey, &run.Feature, &run.Outcome, &run.Model, &run.Effort, &run.ThreadID, &run.TurnID, &duration, &outputBytes, &run.Detail, &run.StartedAt, &completedAt); err != nil {
			return nil, err
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
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.populateLunaRunSteps(ctx, actorID, runs); err != nil {
		return nil, err
	}
	return runs, nil
}

// LunaRunDuration returns a capped millisecond duration suitable for the
// privacy-bounded history record.
func LunaRunDuration(started time.Time) int64 {
	duration := time.Since(started).Milliseconds()
	if duration < 0 {
		return 0
	}
	if duration > 300_000 {
		return 300_000
	}
	return duration
}
