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
func (s *Store) ApplyBulkTaskMutations(ctx context.Context, projectID, actorID, mode string, mutations []BulkTaskMutation, allowClaimOverride bool) (BulkTaskMutationBatch, error) {
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
			result, err := s.applyBulkTaskMutation(ctx, projectID, actorID, mutation, allowClaimOverride)
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
			result, err := s.applyBulkTaskMutationTx(ctx, tx, projectID, actorID, mutation, updatedAt[index], allowClaimOverride)
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

func (s *Store) applyBulkTaskMutation(ctx context.Context, projectID, actorID string, mutation BulkTaskMutation, allowClaimOverride bool) (BulkTaskMutationResult, error) {
	result := BulkTaskMutationResult{}
	updatedAt := now()
	err := s.withImmediateTx(ctx, func(tx dependencySQL) error {
		var err error
		result, err = s.applyBulkTaskMutationTx(ctx, tx, projectID, actorID, mutation, updatedAt, allowClaimOverride)
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

func (s *Store) applyBulkTaskMutationTx(ctx context.Context, tx dependencySQL, projectID, actorID string, mutation BulkTaskMutation, updatedAt string, allowClaimOverride bool) (BulkTaskMutationResult, error) {
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

func bulkClaimGuard(query string, args []any, actorID, updatedAt string, allowClaimOverride bool) (string, []any) {
	if allowClaimOverride {
		return query, args
	}
	return query + ` AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at) <= julianday(?) OR claimed_by=?)`, append(args, updatedAt, actorID)
}

func (s *Store) applyBulkUpdateTx(ctx context.Context, tx dependencySQL, current Task, sourceColumn Column, operation string, input TaskInput, expected int64, actorID, updatedAt string, allowClaimOverride bool) (Task, error) {
	validated, err := validateTaskInput(input, false)
	if err != nil {
		return Task{}, err
	}
	if sourceColumn.ArchivedAt != nil {
		return Task{}, invalid("task is assigned to an archived column", map[string]any{"current": current})
	}
	if validated.ColumnID != nil || validated.Position != nil || validated.Kind != nil || validated.BugSet || validated.Bug != nil {
		return Task{}, invalid("bulk update does not support column, position, kind, or bug changes", nil)
	}
	title, description, priority := current.Title, current.Description, current.Priority
	if validated.Title != nil {
		title = *validated.Title
	}
	if validated.Description != nil {
		description = *validated.Description
	}
	if validated.Priority != nil {
		priority = *validated.Priority
	}
	assignee := ""
	if current.Assignee != nil {
		assignee = *current.Assignee
	}
	if validated.AssigneeSet {
		if validated.Assignee != nil {
			assignee = strings.TrimSpace(*validated.Assignee)
		} else {
			assignee = ""
		}
	}
	if assignee != "" {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM actors WHERE id=?)`, assignee).Scan(&exists); err != nil {
			return Task{}, err
		}
		if exists == 0 {
			return Task{}, invalid("assignee actor not found", nil)
		}
	}
	dueAt := ""
	if current.DueAt != nil {
		dueAt = *current.DueAt
	}
	if validated.DueAtSet {
		if validated.DueAt != nil {
			dueAt = *validated.DueAt
		} else {
			dueAt = ""
		}
	}
	query := `UPDATE tasks SET title=?, description=?, priority=?, assignee_id=NULLIF(?, ''), due_at=NULLIF(?, ''), version=version+1, updated_at=? WHERE id=? AND version=? AND deleted_at IS NULL AND version < 9223372036854775807`
	args := []any{title, description, priority, assignee, dueAt, updatedAt, current.ID, expected}
	query, args = bulkClaimGuard(query, args, actorID, updatedAt, allowClaimOverride)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return Task{}, mapDependencyLifecycleError(ctx, tx, err, dependencyLifecycleTarget{TaskID: current.ID})
	}
	var changed int64
	if err := tx.QueryRowContext(ctx, `SELECT changes()`).Scan(&changed); err != nil {
		return Task{}, err
	}
	if changed == 0 {
		return Task{}, s.bulkMutationFailure(ctx, tx, current, expected, actorID, false, false, false, allowClaimOverride, updatedAt)
	}
	// The source column can be archived while this transaction waits for the
	// SQLite writer lock. Keep field-only mutations aligned with the current
	// task PATCH path: an archived column never authorizes a task write.
	if _, err := authoritativeColumnStateTx(ctx, tx, current.ColumnID, current.ProjectID); err != nil {
		return Task{}, err
	}
	if validated.LabelsSet {
		if err := replaceTaskLabels(ctx, tx, current.ID, current.ProjectID, validated.Labels); err != nil {
			return Task{}, err
		}
	}
	if _, err := insertEvent(ctx, tx, "task.updated", actorID, current.ProjectID, current.ID, map[string]any{
		"version": expected + 1, "bulk": true, "operation": operation,
	}); err != nil {
		return Task{}, err
	}
	return bulkTaskAfterMutationTx(ctx, tx, current.ID)
}

func (s *Store) applyBulkMoveTx(ctx context.Context, tx dependencySQL, current Task, source Column, input TaskMoveInput, expected int64, actorID string) (Task, error) {
	validated, err := validateTaskMoveInput(input)
	if err != nil {
		return Task{}, err
	}
	destination, err := bulkColumnTx(ctx, tx, validated.DestinationColumnID)
	if err != nil {
		return Task{}, err
	}
	if destination.ProjectID != current.ProjectID {
		return Task{}, invalid("destination column belongs to another project", nil)
	}
	if source.ArchivedAt != nil || destination.ArchivedAt != nil {
		return Task{}, invalid("task columns must not be archived", map[string]any{"current": current})
	}
	if source.ID != validated.ExpectedSourceColumnID {
		return Task{}, conflict("task has changed", map[string]any{"current": current, "expected_source_column_id": validated.ExpectedSourceColumnID})
	}
	if !taskOrderingStateAllowed(source.SemanticState) {
		return Task{}, invalid("task source column is not reorderable", map[string]any{"current": current})
	}
	if destination.SemanticState != "backlog" && destination.SemanticState != "ready" {
		return Task{}, invalid("destination column must have backlog or ready semantic state", nil)
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks
		SET column_id=?,
			position=COALESCE((SELECT MAX(other.position)+1 FROM tasks other WHERE other.column_id=? AND other.deleted_at IS NULL AND other.id<>?), 0),
			claimed_by=CASE WHEN claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) IS NOT NULL AND julianday(claim_expires_at)>julianday('now') THEN claimed_by ELSE NULL END,
			claim_expires_at=CASE WHEN claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) IS NOT NULL AND julianday(claim_expires_at)>julianday('now') THEN claim_expires_at ELSE NULL END,
			version=version+1,
			updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id=? AND version=? AND column_id=? AND deleted_at IS NULL AND completed_at IS NULL AND version < 9223372036854775807
			AND EXISTS (SELECT 1 FROM columns source_column WHERE source_column.id=tasks.column_id AND source_column.project_id=tasks.project_id AND source_column.archived_at IS NULL AND source_column.semantic_state IN ('backlog','ready','active','blocked'))
			AND EXISTS (SELECT 1 FROM columns destination_column WHERE destination_column.id=? AND destination_column.project_id=tasks.project_id AND destination_column.archived_at IS NULL AND destination_column.semantic_state IN ('backlog','ready'))
			AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at) IS NULL OR julianday(claim_expires_at)<=julianday('now'))`,
		validated.DestinationColumnID, validated.DestinationColumnID, current.ID, current.ID, expected, validated.ExpectedSourceColumnID, validated.DestinationColumnID)
	if err != nil {
		return Task{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return Task{}, s.taskMoveMutationFailure(ctx, tx, current.ID, actorID, expected, validated.ExpectedSourceColumnID, validated.DestinationColumnID)
	}
	var newPosition float64
	var resultingVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT position, version FROM tasks WHERE id=?`, current.ID).Scan(&newPosition, &resultingVersion); err != nil {
		return Task{}, err
	}
	// Re-read both columns after the guarded write so activity carries the
	// same authoritative names, semantic states, and ordering revisions as the
	// current direct move/reorder paths.
	source, err = bulkColumnTx(ctx, tx, source.ID)
	if err != nil {
		return Task{}, err
	}
	destination, err = bulkColumnTx(ctx, tx, destination.ID)
	if err != nil {
		return Task{}, err
	}
	payload := map[string]any{
		"from":           map[string]any{"id": source.ID, "name": source.Name, "semantic_state": source.SemanticState},
		"to":             map[string]any{"id": destination.ID, "name": destination.Name, "semantic_state": destination.SemanticState},
		"from_column_id": source.ID, "to_column_id": destination.ID, "from_column": source.Name, "to_column": destination.Name,
		"from_semantic_state": source.SemanticState, "to_semantic_state": destination.SemanticState,
		"from_column_state": source.SemanticState, "to_column_state": destination.SemanticState,
		"old_position": current.Position, "new_position": newPosition, "version": resultingVersion, "resulting_version": resultingVersion,
		"actor": actorID, "actor_id": actorID, "source": validated.Source, "reason": validated.Reason, "bulk": true,
		"placement": "last", "rebalanced": false,
		"ordering_version": destination.OrderingVersion, "source_ordering_version": source.OrderingVersion,
	}
	if _, err := insertEvent(ctx, tx, "task.moved", actorID, current.ProjectID, current.ID, payload); err != nil {
		return Task{}, err
	}
	return bulkTaskAfterMutationTx(ctx, tx, current.ID)
}

func (s *Store) applyBulkLifecycleTx(ctx context.Context, tx dependencySQL, current Task, source Column, operation string, expected int64, actorID, note string, requireClaim, allowClaimOverride bool) (Task, error) {
	state := "blocked"
	eventType := "task.blocked"
	if operation == "complete" {
		state, eventType = "completed", "task.completed"
	}
	if current.Kind == bugKind && state == "completed" {
		return Task{}, invalid("bugs must be completed with a resolution", nil)
	}
	if current.Kind == bugKind {
		bug, bugErr := bugFromRow(tx.QueryRowContext(ctx, `SELECT reporter_id, severity, actual_behavior, expected_behavior, reproduction_steps, environment, affected_version, resolution, resolved_by, resolved_at, duplicate_of FROM bug_details WHERE task_id=?`, current.ID))
		if bugErr == nil && bug.Resolution != nil && state != "completed" {
			return Task{}, invalid("resolved bugs must be reopened before leaving a completed column", nil)
		}
		if bugErr != nil && !errors.Is(bugErr, sql.ErrNoRows) {
			return Task{}, bugErr
		}
	}
	if source.ArchivedAt != nil {
		return Task{}, invalid("task is assigned to an archived column", map[string]any{"current": current})
	}
	target, err := columnBySemanticTx(ctx, tx, current.ProjectID, state)
	if err != nil {
		return Task{}, err
	}
	query := `UPDATE tasks SET column_id=?, completed_at=CASE WHEN ? <> 'completed' THEN NULL WHEN (SELECT semantic_state FROM columns WHERE id=tasks.column_id) = 'completed' THEN tasks.completed_at ELSE strftime('%Y-%m-%dT%H:%M:%fZ','now') END, claimed_by=NULL, claim_expires_at=NULL, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND deleted_at IS NULL`
	args := []any{target.ID, state, current.ID, expected}
	if requireClaim {
		query += ` AND claimed_by=? AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at)>julianday('now')`
		args = append(args, actorID)
	} else if !allowClaimOverride {
		query += ` AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at)<=julianday('now') OR claimed_by=?)`
		args = append(args, actorID)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return Task{}, mapDependencyLifecycleError(ctx, tx, err, dependencyLifecycleTarget{TaskID: current.ID})
	}
	var changed int64
	if err := tx.QueryRowContext(ctx, `SELECT changes()`).Scan(&changed); err != nil {
		return Task{}, err
	}
	if changed == 0 {
		return Task{}, s.bulkMutationFailure(ctx, tx, current, expected, actorID, requireClaim, false, true, allowClaimOverride, now())
	}
	destinationState, err := authoritativeColumnStateTx(ctx, tx, target.ID, current.ProjectID)
	if err != nil {
		return Task{}, err
	}
	if destinationState != state {
		return Task{}, conflict("task destination changed semantic state", map[string]any{"column_id": target.ID, "expected_state": state, "current_state": destinationState})
	}
	sourceState := destinationState
	if current.ColumnID != target.ID {
		sourceState, err = authoritativeColumnStateTx(ctx, tx, current.ColumnID, current.ProjectID)
		if err != nil {
			return Task{}, err
		}
	}
	if err := validateTaskBugLifecycleTx(ctx, tx, current.ID, current.Kind, destinationState); err != nil {
		return Task{}, err
	}
	beforeDependency := dependencyTaskFromTask(current, sourceState)
	checklistStatus := checklistCompletionStatus{}
	if operation == "complete" && destinationState == "completed" {
		checklistStatus, err = checklistCompletionStatusForTaskTx(ctx, tx, current.ProjectID, current.ID)
		if err != nil {
			return Task{}, err
		}
		if err := rejectIncompleteChecklist(checklistStatus); err != nil {
			return Task{}, err
		}
	}
	if strings.TrimSpace(note) != "" {
		commentID := newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, task_id, actor_id, body, created_at, updated_at) VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'), strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, commentID, current.ID, actorID, strings.TrimSpace(note)); err != nil {
			return Task{}, err
		}
		if _, err := insertEvent(ctx, tx, "comment.created", actorID, current.ProjectID, current.ID, map[string]any{"comment_id": commentID, "bulk": true}); err != nil {
			return Task{}, err
		}
	}
	eventPayload := map[string]any{"column_id": target.ID, "bulk": true}
	if operation == "complete" {
		addChecklistCompletionEventFields(eventPayload, checklistStatus)
	}
	if _, err := insertEvent(ctx, tx, eventType, actorID, current.ProjectID, current.ID, eventPayload); err != nil {
		return Task{}, err
	}
	change, stateChanged, err := dependencyTaskStateChange(ctx, tx, beforeDependency)
	if err != nil {
		return Task{}, err
	}
	if stateChanged {
		if err := emitDependencyStateChanges(ctx, tx, actorID, []dependencyStateChange{change}); err != nil {
			return Task{}, err
		}
	}
	return bulkTaskAfterMutationTx(ctx, tx, current.ID)
}

func columnBySemanticTx(ctx context.Context, tx dependencySQL, projectID, state string) (Column, error) {
	column, err := columnFromRow(tx.QueryRowContext(ctx, `SELECT id, project_id, name, semantic_state, position, archived_at, ordering_version, created_at, updated_at, version FROM columns WHERE project_id=? AND semantic_state=? AND archived_at IS NULL ORDER BY position, id LIMIT 1`, projectID, state))
	if errors.Is(err, sql.ErrNoRows) {
		return Column{}, notFound("column not found")
	}
	return column, err
}

func (s *Store) bulkMutationFailure(ctx context.Context, tx dependencySQL, current Task, expected int64, actorID string, requireClaim, move, lifecycle, allowClaimOverride bool, claimAt string) error {
	latest, err := taskFromRow(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks t WHERE t.id=? AND t.deleted_at IS NULL`, current.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return notFound("task not found")
	}
	if err != nil {
		return err
	}
	if latest.Version != expected {
		return conflict("task has changed", map[string]any{"current": latest, "expected_version": expected})
	}
	if !allowClaimOverride {
		if claimAt == "" {
			claimAt = now()
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at)>julianday(?) AND deleted_at IS NULL)`, current.ID, claimAt).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			if requireClaim {
				var owner int
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND claimed_by=? AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at)>julianday(?) AND deleted_at IS NULL)`, current.ID, actorID, claimAt).Scan(&owner); err != nil {
					return err
				}
				if owner == 0 {
					return forbidden("an active claim owned by this actor is required")
				}
			} else if lifecycle {
				return forbidden("only the claim owner may perform this action")
			} else {
				return &Error{Kind: ErrClaimUnavailable, Message: "task is currently claimed by another actor", Details: map[string]any{"task_id": current.ID}}
			}
		}
		if requireClaim {
			return forbidden("an active claim owned by this actor is required")
		}
	}
	if move {
		return conflict("task move could not be applied", map[string]any{"current": latest, "expected_version": expected})
	}
	return conflict("task has changed", map[string]any{"current": latest, "expected_version": expected})
}

func bulkTaskAfterMutationTx(ctx context.Context, tx dependencySQL, id string) (Task, error) {
	task, _, err := resolveBulkTaskTx(ctx, tx, id)
	return task, err
}
