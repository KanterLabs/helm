package store

import (
	"context"
	"database/sql"
	"errors"
)

// Keep project authorization under the same transaction as admission. The
// token and actor project lists independently cap the token's access.
func authorizeAgentAdmission(ctx context.Context, q dependencySQL, projectID, actorID, tokenID string) (bool, error) {
	var canRead bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM json_each(k.scopes) WHERE value='tasks:read')
		FROM tokens k JOIN actors a ON a.id=k.actor_id
		JOIN projects p ON p.id=?
		WHERE k.id=? AND a.id=? AND a.kind='agent' AND a.disabled_at IS NULL
		AND (k.expires_at IS NULL OR julianday(k.expires_at)>julianday('now'))
		AND EXISTS (SELECT 1 FROM json_each(k.scopes) WHERE value='tasks:write')
		AND EXISTS (SELECT 1 FROM json_each(k.scopes) WHERE value='tasks:claim')
		AND (json_array_length(k.project_ids)=0 OR EXISTS (SELECT 1 FROM json_each(k.project_ids) WHERE value=p.id))
		AND (NOT EXISTS (SELECT 1 FROM actor_projects ap WHERE ap.actor_id=a.id)
			OR EXISTS (SELECT 1 FROM actor_projects ap WHERE ap.actor_id=a.id AND ap.project_id=p.id))`, projectID, tokenID, actorID).Scan(&canRead)
	if errors.Is(err, sql.ErrNoRows) {
		return false, forbidden("an authorized agent token for this project is required")
	}
	return canRead, err
}

type agentAdmissionCandidate struct {
	ID, Key, ColumnID, ColumnName, State, Assignee, ClaimedBy string
	Position                                                  float64
	Completed, Archived, Released, Unmet, ClaimActive         bool
}

const admissionUnmetPrerequisites = `EXISTS (SELECT 1 FROM task_dependencies d
	JOIN tasks prerequisite ON prerequisite.id=d.prerequisite_task_id
	JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
	WHERE d.task_id=t.id AND prerequisite.project_id=t.project_id AND prerequisite.deleted_at IS NULL
	AND (prerequisite.completed_at IS NULL OR prerequisite_column.semantic_state<>'completed'))`

const admissionActiveClaim = `(t.claimed_by IS NOT NULL AND COALESCE(julianday(t.claim_expires_at)>julianday('now'),0))`

func readAgentAdmissionCandidate(ctx context.Context, q dependencySQL, projectID, reference string) (agentAdmissionCandidate, error) {
	var c agentAdmissionCandidate
	err := q.QueryRowContext(ctx, `SELECT t.id, p.key || '-' || t.number, t.column_id, c.name, c.semantic_state,
		COALESCE(t.assignee_id,''), COALESCE(t.claimed_by,''), t.position, t.completed_at IS NOT NULL,
		(c.archived_at IS NOT NULL OR p.archived_at IS NOT NULL), r.released_at IS NOT NULL,
		`+admissionUnmetPrerequisites+`, `+admissionActiveClaim+`
		FROM tasks t JOIN projects p ON p.id=t.project_id JOIN columns c ON c.id=t.column_id
		LEFT JOIN releases r ON r.id=t.release_id
		WHERE p.id=? AND t.deleted_at IS NULL AND (t.id=? OR lower(p.key || '-' || t.number)=lower(?))
		LIMIT 1`, projectID, reference, reference).
		Scan(&c.ID, &c.Key, &c.ColumnID, &c.ColumnName, &c.State, &c.Assignee, &c.ClaimedBy, &c.Position,
			&c.Completed, &c.Archived, &c.Released, &c.Unmet, &c.ClaimActive)
	if errors.Is(err, sql.ErrNoRows) {
		return agentAdmissionCandidate{}, notFound("task not found in this project")
	}
	return c, err
}

func checkAgentAdmissionCandidate(ctx context.Context, q dependencySQL, task agentAdmissionCandidate, actorID string) error {
	details := map[string]any{"task_keys": []string{task.Key}}
	if task.Completed || task.Archived || task.Released || task.Unmet || (task.Assignee != "" && task.Assignee != actorID) ||
		(task.State != "active" && task.State != "ready" && task.State != "backlog") {
		return &Error{Kind: ErrAgentTaskNotEligible, Message: "this task is not eligible; review its owner, state and prerequisites", Details: details}
	}
	if !task.ClaimActive {
		return nil
	}
	if task.ClaimedBy != actorID {
		return &Error{Kind: ErrClaimUnavailable, Message: "another actor already owns an active claim on this task", Details: details}
	}
	// A pre-admission claim has no session receipt and can be adopted by its
	// actor. A bound live claim can only be replayed with its original key.
	var bound bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+agentAdmissionJoin+`
		WHERE t.id=? AND a.actor_id=? AND `+liveAgentAdmission+`)`, task.ID, actorID).Scan(&bound); err != nil {
		return err
	}
	if bound {
		return &Error{Kind: ErrAgentSessionInUse, Message: "the existing admission is still held; use its original receipt or wait for its release", Details: details}
	}
	return nil
}

func findAgentAdmissionDuplicates(ctx context.Context, q dependencySQL, projectID, title string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT p.key || '-' || t.number FROM tasks t
		JOIN projects p ON p.id=t.project_id JOIN columns c ON c.id=t.column_id
		WHERE p.id=? AND p.archived_at IS NULL AND c.archived_at IS NULL AND t.deleted_at IS NULL
		AND t.completed_at IS NULL AND c.semantic_state<>'completed' AND lower(trim(t.title))=lower(?)
		ORDER BY t.number LIMIT 10`, projectID, title)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

type AgentAdmissionSuggestion struct {
	owned           bool
	ID              string `json:"id"`
	Key             string `json:"key"`
	Title           string `json:"title"`
	NextAction      string `json:"next_action,omitempty"`
	FinishCondition string `json:"finish_condition,omitempty"`
}

type AgentAdmissionEligibility struct {
	Tasks []AgentAdmissionSuggestion `json:"tasks"`
}

func (s *Store) AgentAdmissionEligibility(ctx context.Context, projectID, actorID, tokenID string) (AgentAdmissionEligibility, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AgentAdmissionEligibility{}, err
	}
	defer tx.Rollback()
	canRead, err := authorizeAgentAdmission(ctx, tx, projectID, actorID, tokenID)
	if err != nil {
		return AgentAdmissionEligibility{}, err
	}
	if !canRead {
		return AgentAdmissionEligibility{}, forbidden("reading eligible tasks requires tasks:read")
	}
	items, err := agentAdmissionSuggestions(ctx, tx, projectID, actorID, true, "")
	if errors.Is(err, ErrAgentAlreadyActive) {
		err = checkAgentAdmissionActiveTask(ctx, tx, actorID, tokenID, "")
	}
	return AgentAdmissionEligibility{Tasks: items}, err
}

// Recovery of an already Active card does not consume a new board slot.
// Apply both caps for advisory suggestions; the POST repeats the checks with
// the writer lock, so a suggestion never authorizes execution by itself.
func agentAdmissionSuggestions(ctx context.Context, q dependencySQL, projectID, actorID string, checkAgentCapacity bool, excludeTaskID string) ([]AgentAdmissionSuggestion, error) {
	projectCapacityErr := checkProjectActiveCapacityTx(ctx, q, projectID, 1)
	if projectCapacityErr != nil && !errors.Is(projectCapacityErr, ErrProjectCapacityReached) {
		return nil, projectCapacityErr
	}
	if projectCapacityErr == nil {
		if _, err := agentAdmissionActiveColumn(ctx, q, projectID); err != nil {
			if !errors.Is(err, ErrAgentTaskNotEligible) {
				return nil, err
			}
			projectCapacityErr = err
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT t.id, p.key || '-' || t.number, t.title,
		COALESCE(NULLIF(admission.next_action,''), work.next_action, ''), COALESCE(admission.finish_condition, ''),
		COALESCE(t.assignee_id=? OR t.claimed_by=?,0)
		FROM tasks t JOIN projects p ON p.id=t.project_id JOIN columns c ON c.id=t.column_id
		LEFT JOIN releases r ON r.id=t.release_id
		LEFT JOIN task_agent_admissions admission ON admission.task_id=t.id
		LEFT JOIN task_agent_work work ON work.task_id=t.id
		WHERE p.id=? AND p.archived_at IS NULL AND c.archived_at IS NULL AND t.deleted_at IS NULL AND t.completed_at IS NULL
		AND t.id<>? AND (c.semantic_state='active' OR (? AND c.semantic_state='ready'))
		AND r.released_at IS NULL AND (t.assignee_id IS NULL OR t.assignee_id=?)
		AND (NOT `+admissionActiveClaim+` OR (t.claimed_by=? AND (admission.task_id IS NULL OR admission.ended_at IS NOT NULL)))
		AND NOT `+admissionUnmetPrerequisites+`
		AND (NOT ? OR NOT EXISTS(
			SELECT 1 FROM tasks held JOIN columns hc ON hc.id=held.column_id JOIN projects hp ON hp.id=held.project_id
			WHERE held.claimed_by=? AND held.id<>t.id AND held.deleted_at IS NULL AND held.completed_at IS NULL
			AND julianday(held.claim_expires_at)>julianday('now')
			AND hp.archived_at IS NULL AND hc.archived_at IS NULL AND hc.semantic_state='active'
		))
		ORDER BY (t.assignee_id=?) DESC, (t.claimed_by=?) DESC, c.semantic_state='active' DESC,
		CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END, t.number
		LIMIT 10`, actorID, actorID, projectID, excludeTaskID, projectCapacityErr == nil, actorID, actorID, checkAgentCapacity, actorID, actorID, actorID)
	if err != nil {
		return nil, err
	}
	items := make([]AgentAdmissionSuggestion, 0)
	for rows.Next() {
		var candidate AgentAdmissionSuggestion
		if err := rows.Scan(&candidate.ID, &candidate.Key, &candidate.Title, &candidate.NextAction, &candidate.FinishCondition, &candidate.owned); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, candidate)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 && checkAgentCapacity {
		if err := checkAgentActiveTaskTx(ctx, q, actorID, ""); err != nil {
			return nil, err
		}
		if projectCapacityErr != nil {
			return nil, projectCapacityErr
		}
	}
	return items, nil
}

func checkAgentAdmissionActiveTask(ctx context.Context, q dependencySQL, actorID, tokenID, taskID string) error {
	err := checkAgentActiveTaskTx(ctx, q, actorID, taskID)
	if !errors.Is(err, ErrAgentAlreadyActive) {
		return err
	}
	rows, readErr := q.QueryContext(ctx, `SELECT p.key || '-' || t.number
		FROM tasks t JOIN projects p ON p.id=t.project_id JOIN columns c ON c.id=t.column_id
		JOIN tokens k ON k.id=? AND k.actor_id=t.claimed_by
		WHERE t.claimed_by=? AND t.id<>? AND p.archived_at IS NULL
		AND `+activeCardPredicate+` AND `+admissionActiveClaim+`
		AND EXISTS (SELECT 1 FROM json_each(k.scopes) WHERE value='tasks:read')
		AND (json_array_length(k.project_ids)=0 OR EXISTS (SELECT 1 FROM json_each(k.project_ids) WHERE value=p.id))
		AND (NOT EXISTS (SELECT 1 FROM actor_projects ap WHERE ap.actor_id=t.claimed_by)
			OR EXISTS (SELECT 1 FROM actor_projects ap WHERE ap.actor_id=t.claimed_by AND ap.project_id=p.id))
		ORDER BY p.key, t.number LIMIT 10`, tokenID, actorID, taskID)
	if readErr != nil {
		return readErr
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if readErr := rows.Scan(&key); readErr != nil {
			return readErr
		}
		keys = append(keys, key)
	}
	if readErr := rows.Err(); readErr != nil {
		return readErr
	}
	if len(keys) == 0 {
		return err
	}
	return &Error{Kind: err, Message: err.Error(), Details: map[string]any{"task_keys": keys}}
}

func findAgentAdmissionOwnedCandidates(ctx context.Context, q dependencySQL, projectID, actorID, targetID string) ([]string, error) {
	candidates, err := agentAdmissionSuggestions(ctx, q, projectID, actorID, false, targetID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0)
	for _, candidate := range candidates {
		if candidate.owned {
			keys = append(keys, candidate.Key)
		}
	}
	return keys, nil
}

func addProjectAdmissionConflictKeys(ctx context.Context, q dependencySQL, projectID string, err error) error {
	var typed *Error
	if !errors.Is(err, ErrProjectCapacityReached) || !errors.As(err, &typed) {
		return err
	}
	details, ok := typed.Details.(map[string]any)
	if !ok {
		return err
	}
	rows, readErr := q.QueryContext(ctx, `SELECT p.key || '-' || t.number FROM tasks t
		JOIN projects p ON p.id=t.project_id JOIN columns c ON c.id=t.column_id
		WHERE p.id=? AND `+activeCardPredicate+` ORDER BY t.number LIMIT 10`, projectID)
	if readErr != nil {
		return readErr
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if readErr := rows.Scan(&key); readErr != nil {
			return readErr
		}
		keys = append(keys, key)
	}
	if readErr := rows.Err(); readErr != nil {
		return readErr
	}
	details["task_keys"] = keys
	return err
}
