package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// BulkTaskMutationLimit is the maximum number of task intents accepted by one
// project-scoped bulk request.  Keeping the limit in the store makes direct
// callers observe the same bound as the HTTP API.
const BulkTaskMutationLimit = 100

// BulkTaskMutation is one optimistic-concurrency guarded task intent.  The
// HTTP layer normalizes operation-specific JSON into this shape; the store
// repeats validation and all lifecycle checks inside its write transaction.
type BulkTaskMutation struct {
	Reference       string
	ExpectedVersion int64
	Operation       string
	Input           TaskInput
	Move            *TaskMoveInput
	Note            string
	RequireClaim    bool
}

// BulkTaskMutationResult deliberately carries the original error instead of a
// JSON shape.  The HTTP layer maps it through the same least-privilege error
// policy used by single-task mutations.
type BulkTaskMutationResult struct {
	Reference string
	TaskID    string
	Status    string
	Task      *Task
	Err       error
}

// BulkTaskMutationBatch is returned for both partial and atomic execution.
// Atomic failures are represented in Results with every item skipped or
// conflicted after rollback; a malformed envelope remains a top-level error.
type BulkTaskMutationBatch struct {
	Mode    string
	Results []BulkTaskMutationResult
}

// ApplyBulkTaskMutations executes a bounded, project-scoped task batch. Partial
// mode commits each valid item independently and reports mixed outcomes.
// Atomic mode runs every item in one SQLite transaction and rolls the entire
// transaction back when any item fails. Every successful item emits the same
// task activity event as its single-task counterpart.
func (s *Store) ApplyBulkTaskMutations(ctx context.Context, projectID, actorID, mode string, mutations []BulkTaskMutation, allowClaimOverride bool, session ...AgentAdmissionSession) (BulkTaskMutationBatch, error) {
	projectID = strings.TrimSpace(projectID)
	actorID = strings.TrimSpace(actorID)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "partial"
	}
	if mode != "partial" && mode != "atomic" {
		return BulkTaskMutationBatch{}, invalid("mode must be atomic or partial", nil)
	}
	if projectID == "" {
		return BulkTaskMutationBatch{}, invalid("project is required", nil)
	}
	if actorID == "" {
		return BulkTaskMutationBatch{}, invalid("actor is required", nil)
	}
	if len(mutations) == 0 {
		return BulkTaskMutationBatch{}, invalid("mutations must contain at least one item", nil)
	}
	if len(mutations) > BulkTaskMutationLimit {
		return BulkTaskMutationBatch{}, invalid(fmt.Sprintf("mutations must contain at most %d items", BulkTaskMutationLimit), nil)
	}
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return BulkTaskMutationBatch{}, err
	}
	for index := range mutations {
		if err := validateBulkTaskMutation(mutations[index]); err != nil {
			return BulkTaskMutationBatch{}, invalid(fmt.Sprintf("mutation %d: %s", index+1, err), nil)
		}
	}

	batch := BulkTaskMutationBatch{Mode: mode, Results: make([]BulkTaskMutationResult, len(mutations))}
	for index, mutation := range mutations {
		batch.Results[index].Reference = mutation.Reference
	}
	if mode == "partial" {
		for index, mutation := range mutations {
			result, err := s.applyBulkTaskMutation(ctx, projectID, actorID, mutation, allowClaimOverride, session)
			result.Reference = mutation.Reference
			result.Status = bulkMutationStatus(err)
			result.Err = err
			batch.Results[index] = result
		}
		return batch, nil
	}

	failedIndex := -1
	var failedErr error
	updatedAt := make([]string, len(mutations))
	for index := range updatedAt {
		// Capture field-mutation timestamps before BEGIN IMMEDIATE, matching the
		// direct task PATCH cutoff even when acquiring the SQLite writer lock
		// waits behind another writer.
		updatedAt[index] = now()
	}
	err := s.withImmediateTx(ctx, func(tx dependencySQL) error {
		for index, mutation := range mutations {
			result, err := s.applyBulkTaskMutationTx(ctx, tx, projectID, actorID, mutation, updatedAt[index], allowClaimOverride, session)
			result.Reference = mutation.Reference
			batch.Results[index] = result
			if err != nil {
				failedIndex, failedErr = index, err
				return err
			}
		}
		return nil
	})
	if err != nil {
		if failedErr == nil {
			failedErr = err
		}
		if failedIndex >= 0 && batch.Results[failedIndex].TaskID != "" && (errors.Is(failedErr, ErrConflict) || errors.Is(failedErr, ErrClaimUnavailable)) {
			if current, readErr := s.GetTask(ctx, batch.Results[failedIndex].TaskID); readErr == nil {
				attachBulkCurrent(failedErr, current)
			}
		}
		for index := range batch.Results {
			result := &batch.Results[index]
			result.Task = nil
			if index == failedIndex {
				result.Status = bulkMutationStatus(failedErr)
				result.Err = failedErr
				continue
			}
			result.Status = "skipped"
			result.Err = &Error{Kind: ErrConflict, Message: "atomic batch rolled back", Details: map[string]any{"reason": "atomic_rollback"}}
		}
		return batch, nil
	}
	for index := range batch.Results {
		result := &batch.Results[index]
		if result.TaskID == "" {
			result.Status = "skipped"
			result.Err = notFound("task not found")
			continue
		}
		updated, readErr := s.GetTask(ctx, result.TaskID)
		if readErr != nil {
			return BulkTaskMutationBatch{}, readErr
		}
		result.Task = &updated
		result.Status = "applied"
	}
	return batch, nil
}

func validateBulkTaskMutation(mutation BulkTaskMutation) error {
	if strings.TrimSpace(mutation.Reference) == "" {
		return errors.New("task is required")
	}
	if mutation.ExpectedVersion <= 0 {
		return ErrPrecondition
	}
	operation := strings.ToLower(strings.TrimSpace(mutation.Operation))
	switch operation {
	case "move":
		if mutation.Move == nil {
			return errors.New("move input is required")
		}
		_, err := validateTaskMoveInput(*mutation.Move)
		return err
	case "assign", "priority", "labels", "due_at", "complete", "block":
		if operation == "complete" || operation == "block" {
			if len(mutation.Note) > 10000 {
				return errors.New("action note is too long")
			}
			return nil
		}
		if operation == "assign" && !mutation.Input.AssigneeSet {
			return errors.New("assignee is required")
		}
		if operation == "priority" && mutation.Input.Priority == nil {
			return errors.New("priority is required")
		}
		if operation == "labels" && !mutation.Input.LabelsSet {
			return errors.New("labels are required")
		}
		if operation == "due_at" && !mutation.Input.DueAtSet {
			return errors.New("due_at is required")
		}
		_, err := validateTaskInput(mutation.Input, false)
		return err
	default:
		return errors.New("operation must be move, assign, priority, labels, due_at, complete, or block")
	}
}

func bulkMutationStatus(err error) string {
	if err == nil {
		return "applied"
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrClaimUnavailable) || errors.Is(err, ErrUnmetDependencies) || errors.Is(err, ErrDependencyInUse) || errors.Is(err, ErrChecklistIncomplete) {
		return "conflict"
	}
	return "skipped"
}

func (s *Store) applyBulkTaskMutation(ctx context.Context, projectID, actorID string, mutation BulkTaskMutation, allowClaimOverride bool, session []AgentAdmissionSession) (BulkTaskMutationResult, error) {
	result := BulkTaskMutationResult{}
	updatedAt := now()
	err := s.withImmediateTx(ctx, func(tx dependencySQL) error {
		var err error
		result, err = s.applyBulkTaskMutationTx(ctx, tx, projectID, actorID, mutation, updatedAt, allowClaimOverride, session)
		return err
	})
	if err != nil {
		if result.TaskID != "" && (errors.Is(err, ErrConflict) || errors.Is(err, ErrClaimUnavailable)) {
			if current, readErr := s.GetTask(ctx, result.TaskID); readErr == nil {
				attachBulkCurrent(err, current)
			}
		}
		return result, err
	}
	if result.TaskID == "" {
		return result, notFound("task not found")
	}
	updated, err := s.GetTask(ctx, result.TaskID)
	if err != nil {
		return result, err
	}
	result.Task = &updated
	return result, nil
}

func attachBulkCurrent(err error, current Task) {
	var typed *Error
	if !errors.As(err, &typed) {
		return
	}
	details, ok := typed.Details.(map[string]any)
	if !ok || details == nil {
		details = make(map[string]any)
	}
	details["current"] = current
	typed.Details = details
}

func resolveBulkTaskTx(ctx context.Context, tx dependencySQL, reference string) (Task, Column, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.deleted_at IS NULL AND (t.id=? OR lower(p.key || '-' || CAST(t.number AS TEXT))=lower(?)) LIMIT 1`, strings.TrimSpace(reference), strings.TrimSpace(reference))
	task, err := taskFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, Column{}, notFound("task not found")
	}
	if err != nil {
		return Task{}, Column{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT key FROM projects WHERE id=?`, task.ProjectID).Scan(&task.Key); err != nil {
		return Task{}, Column{}, err
	}
	task.Key = fmt.Sprintf("%s-%d", task.Key, task.Number)
	column, err := bulkColumnTx(ctx, tx, task.ColumnID)
	if err != nil {
		return Task{}, Column{}, err
	}
	return task, column, nil
}

func bulkColumnTx(ctx context.Context, tx dependencySQL, id string) (Column, error) {
	// Keep the bulk path on the same current column shape as the ordering and
	// administration paths.  In particular, archived_at and ordering_version
	// were added after the original bulk implementation; omitting either field
	// makes a successful mutation fail while scanning its source/destination.
	column, err := columnFromRow(tx.QueryRowContext(ctx, `SELECT id, project_id, name, semantic_state, position, archived_at, ordering_version, created_at, updated_at, version FROM columns WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Column{}, notFound("column not found")
	}
	return column, err
}

func (s *Store) applyBulkTaskMutationTx(ctx context.Context, tx dependencySQL, projectID, actorID string, mutation BulkTaskMutation, updatedAt string, allowClaimOverride bool, session []AgentAdmissionSession) (BulkTaskMutationResult, error) {
	current, sourceColumn, err := resolveBulkTaskTx(ctx, tx, mutation.Reference)
	result := BulkTaskMutationResult{TaskID: current.ID}
	if err != nil {
		return result, err
	}
	if current.ProjectID != projectID {
		// Do not echo an out-of-scope task ID in the per-item result. The
		// reference is already supplied by the caller; returning the resolved
		// opaque ID would disclose another project's task existence.
		result.TaskID = ""
		return result, forbidden("task is outside the requested project")
	}
	if err := checkAgentAdmissionSessionTx(ctx, tx, current.ID, actorID, session); err != nil {
		return result, err
	}
	operation := strings.ToLower(strings.TrimSpace(mutation.Operation))
	switch operation {
	case "move":
		updated, err := s.applyBulkMoveTx(ctx, tx, current, sourceColumn, *mutation.Move, mutation.ExpectedVersion, actorID)
		if err != nil {
			return result, err
		}
		result.Task = &updated
		return result, nil
	case "complete", "block":
		updated, err := s.applyBulkLifecycleTx(ctx, tx, current, sourceColumn, operation, mutation.ExpectedVersion, actorID, mutation.Note, mutation.RequireClaim, allowClaimOverride)
		if err != nil {
			return result, err
		}
		result.Task = &updated
		return result, nil
	case "assign", "priority", "labels", "due_at":
		updated, err := s.applyBulkUpdateTx(ctx, tx, current, sourceColumn, operation, mutation.Input, mutation.ExpectedVersion, actorID, updatedAt, allowClaimOverride)
		if err != nil {
			return result, err
		}
		result.Task = &updated
		return result, nil
	default:
		return result, errors.New("unsupported bulk operation")
	}
}

func columnBySemanticTx(ctx context.Context, tx dependencySQL, projectID, state string) (Column, error) {
	column, err := columnFromRow(tx.QueryRowContext(ctx, `SELECT id, project_id, name, semantic_state, position, archived_at, ordering_version, created_at, updated_at, version FROM columns WHERE project_id=? AND semantic_state=? AND archived_at IS NULL ORDER BY position, id LIMIT 1`, projectID, state))
	if errors.Is(err, sql.ErrNoRows) {
		return Column{}, notFound("column not found")
	}
	return column, err
}

func bulkTaskAfterMutationTx(ctx context.Context, tx dependencySQL, id string) (Task, error) {
	task, _, err := resolveBulkTaskTx(ctx, tx, id)
	return task, err
}
