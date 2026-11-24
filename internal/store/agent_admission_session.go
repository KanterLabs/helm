package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type AgentAdmissionSession struct {
	TokenID   string
	SessionID string
	Fence     string
}

var ErrAgentAdmissionNotFound = errors.New("no current agent admission")

// A session binding must still own a live task claim in Active before it is a
// valid execution fence. Card or project archival ends that binding.
const liveAgentAdmission = `a.ended_at IS NULL AND t.deleted_at IS NULL AND t.completed_at IS NULL
	AND t.claimed_by=a.actor_id AND julianday(t.claim_expires_at)>julianday('now')
	AND projects.archived_at IS NULL AND columns.archived_at IS NULL AND columns.semantic_state='active'`

const agentAdmissionJoin = ` FROM task_agent_admissions a JOIN tasks t ON t.id=a.task_id
	JOIN projects ON projects.id=t.project_id JOIN columns ON columns.id=t.column_id `

func (s *Store) CurrentAgentAdmission(ctx context.Context, projectID, actorID, tokenID, sessionID string) (AgentAdmission, error) {
	if _, err := validateAgentWorkOperationID(sessionID); err != nil || strings.TrimSpace(sessionID) != sessionID {
		return AgentAdmission{}, invalid("session_id must contain 1 to 128 safe identifier characters", nil)
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AgentAdmission{}, err
	}
	defer tx.Rollback()
	canRead, err := authorizeAgentAdmission(ctx, tx, projectID, actorID, tokenID)
	if err != nil {
		return AgentAdmission{}, err
	}
	admitted, err := readLiveAgentAdmission(ctx, tx, projectID, actorID, tokenID, sessionID)
	if !canRead {
		redactAgentAdmission(&admitted)
	}
	return admitted, err
}

func readLiveAgentAdmission(ctx context.Context, q dependencySQL, projectID, actorID, tokenID, sessionID string) (AgentAdmission, error) {
	var result AgentAdmission
	err := q.QueryRowContext(ctx, `SELECT t.id, t.version, projects.key || '-' || t.number, t.title, t.project_id, t.column_id, t.claimed_by, t.claim_expires_at,
		a.action, a.session_id, a.fence, a.next_action, a.finish_condition`+agentAdmissionJoin+`
		WHERE t.project_id=? AND a.actor_id=? AND a.token_id=? AND a.session_id=? AND `+liveAgentAdmission,
		projectID, actorID, tokenID, sessionID).
		Scan(&result.Task.ID, &result.Task.Version, &result.Task.Key, &result.Task.Title, &result.Task.ProjectID,
			&result.Task.ColumnID, &result.Task.ClaimedBy, &result.Task.ClaimExpiresAt, &result.Action,
			&result.SessionID, &result.Fence, &result.NextAction, &result.FinishCondition)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentAdmission{}, &Error{Kind: ErrAgentAdmissionNotFound, Message: "this session has no current admission in the project"}
	}
	return result, err
}

func checkAgentAdmissionSessionUnused(ctx context.Context, q dependencySQL, tokenID, sessionID string) error {
	var exists bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+agentAdmissionJoin+`
		WHERE a.token_id=? AND a.session_id=? AND `+liveAgentAdmission+`)`, tokenID, sessionID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return &Error{Kind: ErrAgentSessionInUse, Message: "this session already owns an active admission; use its existing receipt"}
	}
	return nil
}

func rejectFencedLegacyClaimTx(ctx context.Context, q dependencySQL, taskID, actorID string) error {
	var bound bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+agentAdmissionJoin+`
		WHERE t.id=? AND a.actor_id=? AND a.token_id<>'' AND t.claimed_by=a.actor_id
		AND julianday(t.claim_expires_at)>julianday('now'))`, taskID, actorID).Scan(&bound); err != nil {
		return err
	}
	if bound {
		return &Error{Kind: ErrAgentSessionInUse, Message: "this task has a live session admission; renew or release that admission"}
	}
	return nil
}

// checkAgentAdmissionSessionTx authorizes a beta task's mutating operation
// against its live session binding. Call it under the writer lock, before a
// terminal action clears the claim. Existing unfenced claims keep working.
func checkAgentAdmissionSessionTx(ctx context.Context, q dependencySQL, taskID, actorID string, session []AgentAdmissionSession) error {
	var tokenID, sessionID, fence, projectID string
	var live bool
	err := q.QueryRowContext(ctx, `SELECT a.token_id, a.session_id, a.fence, t.project_id,
		(`+liveAgentAdmission+`)`+agentAdmissionJoin+`
		WHERE t.id=? AND a.actor_id=? AND a.token_id<>'' AND t.claimed_by=a.actor_id
		AND julianday(t.claim_expires_at)>julianday('now')`, taskID, actorID).Scan(&tokenID, &sessionID, &fence, &projectID, &live)
	// Sending an empty header is equivalent to a legacy request. A client that
	// supplies an ID must still own a live binding and cannot mutate stale work.
	if errors.Is(err, sql.ErrNoRows) {
		if len(session) == 0 || (session[0].SessionID == "" && session[0].Fence == "") {
			return nil
		}
		return forbidden("this session no longer owns the task admission")
	}
	if err != nil {
		return err
	}
	if !live {
		return forbidden("this session no longer owns the task admission")
	}
	if len(session) == 0 || session[0].TokenID != tokenID || session[0].SessionID != sessionID || session[0].Fence != fence {
		return forbidden("the matching agent admission session is required")
	}
	_, err = authorizeAgentAdmission(ctx, q, projectID, actorID, tokenID)
	return err
}

func redactAgentAdmission(result *AgentAdmission) {
	result.Task = AgentAdmissionTask{
		ID: result.Task.ID, Version: result.Task.Version, ClaimExpiresAt: result.Task.ClaimExpiresAt,
	}
}

func redactAgentAdmissionError(err error) error {
	var typed *Error
	if errors.As(err, &typed) {
		if details, ok := typed.Details.(map[string]any); ok {
			delete(details, "task_keys")
		}
	}
	return err
}

// detachAgentAdmissionForLegacyClaimTx retains the work plan as history while
// preventing a newly issued legacy claim from inheriting an expired fence.
func detachAgentAdmissionForLegacyClaimTx(ctx context.Context, q dependencySQL, taskID string) error {
	_, err := q.ExecContext(ctx, `UPDATE task_agent_admissions SET token_id='', session_id='', fence=''
		WHERE task_id=? AND ended_at IS NOT NULL`, taskID)
	return err
}
