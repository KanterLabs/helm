package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type AgentAdmissionInput struct {
	SessionID       string  `json:"session_id"`
	TaskKey         string  `json:"task_key,omitempty"`
	Title           *string `json:"title,omitempty"`
	Description     *string `json:"description,omitempty"`
	Priority        *string `json:"priority,omitempty"`
	NextAction      string  `json:"next_action"`
	FinishCondition string  `json:"finish_condition"`
	LeaseSeconds    *int64  `json:"lease_seconds,omitempty"`
}

// AgentAdmission represents the state at admission for a POST receipt and the
// live lease at the time of an authoritative GET.
type AgentAdmission struct {
	Task            AgentAdmissionTask `json:"task"`
	Action          string             `json:"action"`
	SessionID       string             `json:"session_id"`
	Fence           string             `json:"fence"`
	NextAction      string             `json:"next_action"`
	FinishCondition string             `json:"finish_condition"`
}

// Identifiers and optimistic version stay usable by write-and-claim-only
// credentials. Project visibility and tasks:read control the other fields.
type AgentAdmissionTask struct {
	ID             string `json:"id"`
	Version        int64  `json:"version"`
	Key            string `json:"key,omitempty"`
	Title          string `json:"title,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	ColumnID       string `json:"column_id,omitempty"`
	ClaimedBy      string `json:"claimed_by,omitempty"`
	ClaimExpiresAt string `json:"claim_expires_at"`
}

type AgentAdmissionReceipt struct {
	Key         string
	RequestHash string
	Path        string
}

var (
	ErrAgentEligibleTaskExists = errors.New("eligible task exists")
	ErrAgentTaskNotEligible    = errors.New("task cannot be admitted")
	ErrAgentSessionInUse       = errors.New("agent session already has an admission")
)

func validateAgentAdmissionInput(input AgentAdmissionInput) (AgentAdmissionInput, time.Duration, error) {
	if _, err := validateAgentWorkOperationID(input.SessionID); err != nil || strings.TrimSpace(input.SessionID) != input.SessionID {
		return input, 0, invalid("session_id must contain 1 to 128 safe identifier characters", nil)
	}
	input.NextAction = strings.TrimSpace(input.NextAction)
	input.FinishCondition = strings.TrimSpace(input.FinishCondition)
	for _, field := range []struct{ name, value string }{
		{"next_action", input.NextAction},
		{"finish_condition", input.FinishCondition},
	} {
		if field.value == "" || utf8.RuneCountInString(field.value) > 1000 {
			return input, 0, invalid(field.name+" must contain 1 to 1000 characters", nil)
		}
	}
	input.TaskKey = strings.TrimSpace(input.TaskKey)
	if (input.Title == nil) == (input.TaskKey == "") {
		return input, 0, invalid("provide either a title to start or a task_key to resume", nil)
	}
	if len(input.TaskKey) > 200 {
		return input, 0, invalid("task_key is too long", nil)
	}
	if input.Title == nil {
		if input.Description != nil || input.Priority != nil {
			return input, 0, invalid("description and priority require a new task title", nil)
		}
	} else {
		task, err := validateTaskInput(TaskInput{Title: input.Title, Description: input.Description, Priority: input.Priority}, true)
		if err != nil {
			return input, 0, err
		}
		input.Title, input.Description, input.Priority = task.Title, task.Description, task.Priority
	}
	if input.LeaseSeconds == nil {
		return input, DefaultTaskClaimDuration, nil
	}
	seconds := *input.LeaseSeconds
	if seconds < int64(MinTaskClaimDuration/time.Second) || seconds > int64(MaxTaskClaimDuration/time.Second) {
		return input, 0, invalid("lease duration must be between 30 and 604800 seconds", nil)
	}
	return input, time.Duration(seconds) * time.Second, nil
}

// AdmitAgentWork stores assignment, board placement, events and the successful
// idempotency receipt in the same writer transaction. A lost HTTP response can
// replay the receipt without performing a second admission.
func (s *Store) AdmitAgentWork(ctx context.Context, projectID, actorID, tokenID string, input AgentAdmissionInput, receipt AgentAdmissionReceipt) (AgentAdmission, error) {
	input, duration, err := validateAgentAdmissionInput(input)
	if err != nil {
		return AgentAdmission{}, err
	}
	if receipt.Key == "" || receipt.Path == "" || receipt.RequestHash == "" {
		return AgentAdmission{}, invalid("an idempotency key is required", nil)
	}

	var admitted AgentAdmission
	err = s.withImmediateTx(ctx, func(tx dependencySQL) error {
		canRead, err := authorizeAgentAdmission(ctx, tx, projectID, actorID, tokenID)
		if err != nil {
			return err
		}
		stored, found, err := readAgentAdmissionReceipt(ctx, tx, actorID, receipt)
		if err != nil || found {
			admitted = stored
			if !canRead {
				redactAgentAdmission(&admitted)
			}
			return err
		}

		admitted, err = admitAgentWorkTx(ctx, tx, projectID, actorID, tokenID, input, duration)
		if err != nil {
			if !canRead {
				return redactAgentAdmissionError(err)
			}
			return err
		}
		if !canRead {
			redactAgentAdmission(&admitted)
		}
		data, err := json.Marshal(admitted)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO idempotency_keys(actor_id, key, method, path, request_hash, status, response_body, created_at)
			VALUES (?, ?, 'POST', ?, ?, 200, ?, ?)`,
			actorID, receipt.Key, receipt.Path, receipt.RequestHash, `{"data":`+string(data)+`}`, now())
		return err
	})
	if err != nil {
		return AgentAdmission{}, err
	}
	return admitted, nil
}

func admitAgentWorkTx(ctx context.Context, tx dependencySQL, projectID, actorID, tokenID string, input AgentAdmissionInput, duration time.Duration) (AgentAdmission, error) {
	var noAdmission AgentAdmission
	var archived bool
	if err := tx.QueryRowContext(ctx, `SELECT archived_at IS NOT NULL FROM projects WHERE id=?`, projectID).Scan(&archived); err != nil {
		return noAdmission, err
	}
	if archived {
		return noAdmission, &Error{Kind: ErrAgentTaskNotEligible, Message: "reopen the project before admitting work"}
	}
	task, action := agentAdmissionCandidate{}, "started"
	added := 1
	if input.Title == nil {
		var err error
		task, err = readAgentAdmissionCandidate(ctx, tx, projectID, input.TaskKey)
		if err != nil {
			return noAdmission, err
		}
		if err := checkAgentAdmissionCandidate(ctx, tx, task, actorID); err != nil {
			return noAdmission, err
		}
		action = "resumed"
		if task.State == "active" {
			added = 0
		}
	}
	if err := checkAgentAdmissionActiveTask(ctx, tx, actorID, tokenID, task.ID); err != nil {
		return noAdmission, err
	}
	if input.Title == nil && task.Assignee != actorID && task.ClaimedBy != actorID {
		owned, err := findAgentAdmissionOwnedCandidates(ctx, tx, projectID, actorID, task.ID)
		if err != nil {
			return noAdmission, err
		}
		if len(owned) > 0 {
			return noAdmission, &Error{Kind: ErrAgentEligibleTaskExists, Message: "resume an existing task already owned by this agent", Details: map[string]any{"task_keys": owned}}
		}
	}
	if err := checkAgentAdmissionSessionUnused(ctx, tx, tokenID, input.SessionID); err != nil {
		return noAdmission, err
	}

	if input.Title != nil {
		duplicates, err := findAgentAdmissionDuplicates(ctx, tx, projectID, *input.Title)
		if err != nil {
			return noAdmission, err
		}
		if len(duplicates) > 0 {
			return noAdmission, &Error{Kind: ErrAgentEligibleTaskExists, Message: "an open task already has this title; review it before starting new work", Details: map[string]any{"task_keys": duplicates}}
		}
	}
	if err := checkProjectActiveCapacityTx(ctx, tx, projectID, added); err != nil {
		return noAdmission, addProjectAdmissionConflictKeys(ctx, tx, projectID, err)
	}
	if input.Title != nil {
		eligible, err := agentAdmissionSuggestions(ctx, tx, projectID, actorID, false, "")
		if err != nil {
			return noAdmission, err
		}
		if len(eligible) > 0 {
			keys := make([]string, 0, len(eligible))
			for _, item := range eligible {
				keys = append(keys, item.Key)
			}
			return noAdmission, &Error{Kind: ErrAgentEligibleTaskExists, Message: "review eligible project work before starting a new task", Details: map[string]any{"task_keys": keys}}
		}
	}

	if action == "started" {
		var err error
		task, err = createAgentAdmissionTask(ctx, tx, projectID, actorID, input)
		if err != nil {
			return noAdmission, err
		}
	}
	if err := claimAgentAdmissionTask(ctx, tx, projectID, actorID, task, duration); err != nil {
		return noAdmission, err
	}
	fence := newID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_agent_admissions(task_id, actor_id, token_id, session_id, fence, action, next_action, finish_condition)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(task_id) DO UPDATE SET actor_id=excluded.actor_id, token_id=excluded.token_id,
		session_id=excluded.session_id, fence=excluded.fence, action=excluded.action, next_action=excluded.next_action,
		finish_condition=excluded.finish_condition, ended_at=NULL`, task.ID, actorID, tokenID, input.SessionID, fence, action, input.NextAction, input.FinishCondition); err != nil {
		return noAdmission, err
	}
	return readLiveAgentAdmission(ctx, tx, projectID, actorID, tokenID, input.SessionID)
}

func readAgentAdmissionReceipt(ctx context.Context, tx dependencySQL, actorID string, request AgentAdmissionReceipt) (AgentAdmission, bool, error) {
	var method, path, hash, body string
	err := tx.QueryRowContext(ctx, `SELECT method, path, request_hash, response_body FROM idempotency_keys WHERE actor_id=? AND key=?`,
		actorID, request.Key).Scan(&method, &path, &hash, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentAdmission{}, false, nil
	}
	if err != nil {
		return AgentAdmission{}, false, err
	}
	if method != "POST" || path != request.Path || hash != request.RequestHash {
		return AgentAdmission{}, true, conflict("idempotency key was already used for a different request", nil)
	}
	var receipt struct {
		Data AgentAdmission `json:"data"`
	}
	err = json.Unmarshal([]byte(body), &receipt)
	return receipt.Data, true, err
}

func createAgentAdmissionTask(ctx context.Context, tx dependencySQL, projectID, actorID string, input AgentAdmissionInput) (agentAdmissionCandidate, error) {
	column, err := agentAdmissionActiveColumn(ctx, tx, projectID)
	if err != nil {
		return agentAdmissionCandidate{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_counters(project_id, next_number) VALUES (?, 2)
		ON CONFLICT(project_id) DO UPDATE SET next_number=project_counters.next_number+1`, projectID); err != nil {
		return agentAdmissionCandidate{}, err
	}
	var number int
	if err := tx.QueryRowContext(ctx, `SELECT next_number-1 FROM project_counters WHERE project_id=?`, projectID).Scan(&number); err != nil {
		return agentAdmissionCandidate{}, err
	}
	priority := "normal"
	if input.Priority != nil {
		priority = *input.Priority
	}
	var position float64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position)+1,0) FROM tasks WHERE column_id=? AND deleted_at IS NULL`, column.ID).Scan(&position); err != nil {
		return agentAdmissionCandidate{}, err
	}
	id, created := newID(), now()
	if err := txExecTaskCreate(ctx, tx, id, projectID, number, column.ID, defaultTaskKind, *input.Title,
		nullableStringValue(input.Description), priority, position, "", "", nil, created, "", ""); err != nil {
		return agentAdmissionCandidate{}, err
	}
	if _, err := insertEvent(ctx, tx, "task.created", actorID, projectID, id, map[string]any{"number": number}); err != nil {
		return agentAdmissionCandidate{}, err
	}
	return agentAdmissionCandidate{ID: id, ColumnID: column.ID, ColumnName: column.Name, State: "active"}, nil
}

func agentAdmissionActiveColumn(ctx context.Context, tx dependencySQL, projectID string) (Column, error) {
	column, err := columnFromRow(tx.QueryRowContext(ctx, columnSelect+` WHERE project_id=? AND semantic_state='active'
		AND archived_at IS NULL ORDER BY position, id LIMIT 1`, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return Column{}, &Error{Kind: ErrAgentTaskNotEligible, Message: "the project has no available Active column"}
	}
	return column, err
}

func claimAgentAdmissionTask(ctx context.Context, tx dependencySQL, projectID, actorID string, task agentAdmissionCandidate, duration time.Duration) error {
	destination := Column{ID: task.ColumnID, Name: task.ColumnName, SemanticState: task.State}
	if task.State != "active" {
		var err error
		destination, err = agentAdmissionActiveColumn(ctx, tx, projectID)
		if err != nil {
			return err
		}
	}
	var expires string
	var position float64
	var version int64
	err := tx.QueryRowContext(ctx, `UPDATE tasks SET
		column_id=?,
		position=CASE WHEN column_id=? THEN position ELSE COALESCE((SELECT MAX(t.position)+1 FROM tasks t WHERE t.column_id=? AND t.deleted_at IS NULL),0) END,
		claimed_by=?, claim_expires_at=strftime('%Y-%m-%dT%H:%M:%fZ','now',?), version=version+1,
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id=? RETURNING claim_expires_at, position, version`,
		destination.ID, destination.ID, destination.ID, actorID, fmt.Sprintf("+%d seconds", int64(duration/time.Second)), task.ID).Scan(&expires, &position, &version)
	if err != nil {
		return mapDependencyLifecycleError(ctx, tx, err, dependencyLifecycleTarget{TaskID: task.ID})
	}
	if _, err := insertEvent(ctx, tx, "task.claimed", actorID, projectID, task.ID, map[string]any{"expires_at": expires}); err != nil {
		return err
	}
	if task.ColumnID == destination.ID {
		return nil
	}
	_, err = insertEvent(ctx, tx, "task.moved", actorID, projectID, task.ID, map[string]any{
		"from":           map[string]any{"id": task.ColumnID, "name": task.ColumnName, "semantic_state": task.State},
		"to":             map[string]any{"id": destination.ID, "name": destination.Name, "semantic_state": destination.SemanticState},
		"from_column_id": task.ColumnID, "to_column_id": destination.ID,
		"from_column": task.ColumnName, "to_column": destination.Name,
		"from_semantic_state": task.State, "to_semantic_state": destination.SemanticState,
		"old_position": task.Position, "new_position": position, "version": version, "source": "agent_admission",
	})
	return err
}
