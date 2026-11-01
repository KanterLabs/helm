package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) UpdateTask(ctx context.Context, id string, input TaskInput, expected int64, actorID string) (Task, error) {
	return s.UpdateTaskWithClaimOverride(ctx, id, input, expected, actorID, false)
}

func validateTaskBugLifecycleTx(ctx context.Context, tx dependencySQL, taskID, kind, destinationState string) error {
	if kind != bugKind {
		return nil
	}
	var resolution sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT resolution FROM bug_details WHERE task_id=?`, taskID).Scan(&resolution)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	resolved := err == nil && resolution.Valid && strings.TrimSpace(resolution.String) != ""
	if destinationState == "completed" && !resolved {
		return invalid("bugs must be resolved before entering a completed column", nil)
	}
	if destinationState != "completed" && resolved {
		return invalid("resolved bugs must be reopened before leaving a completed column", nil)
	}
	return nil
}

// UpdateTaskWithClaimOverride applies a task patch while atomically honoring
// an active claim owned by another actor. The override is deliberately
// explicit so a bearer token cannot inherit a human administrator's power.
func (s *Store) UpdateTaskWithClaimOverride(ctx context.Context, id string, input TaskInput, expected int64, actorID string, allowClaimOverride bool) (Task, error) {
	validated, err := validateTaskInput(input, false)
	if err != nil {
		return Task{}, err
	}
	if expected <= 0 {
		return Task{}, ErrPrecondition
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	parentSet := validated.ParentSet || validated.ParentTaskID != nil
	parentID := ""
	if current.ParentTaskID != nil {
		parentID = *current.ParentTaskID
	}
	var parent *hierarchyTask
	if parentSet {
		parentID = ""
		if validated.ParentTaskID != nil {
			resolved, parentErr := resolveHierarchyTask(ctx, s.DB, *validated.ParentTaskID)
			if parentErr != nil {
				return Task{}, parentErr
			}
			if resolved.ProjectID != current.ProjectID {
				return Task{}, hierarchyInvalid(ErrHierarchyCrossProject, "parent and child must belong to the same project", map[string]any{
					"child_project_id":  current.ProjectID,
					"parent_project_id": resolved.ProjectID,
				}, ErrInvalid)
			}
			parent = &resolved
			parentID = resolved.ID
		}
	}
	parentChanged := parentSet && parentID != nullableStringValue(current.ParentTaskID)
	releaseSet := validated.ReleaseSet || validated.ReleaseIDSet || validated.ReleaseID != nil
	releaseID := nullableStringValue(current.ReleaseID)
	if releaseSet {
		releaseID = ""
		if validated.ReleaseID != nil {
			releaseID = *validated.ReleaseID
		}
	}
	releaseChanged := releaseSet && releaseID != nullableStringValue(current.ReleaseID)
	if current.Kind == "" {
		// Databases opened before migration 008 are upgraded before serving
		// requests, but retain the safe default for direct store test fixtures.
		current.Kind = defaultTaskKind
	}
	kind := current.Kind
	if validated.Kind != nil {
		kind = *validated.Kind
	}
	bugMutation := validated.BugSet || validated.Bug != nil
	// Supplying bug details is an ergonomic shorthand for a task-to-bug
	// conversion for direct store callers. HTTP clients normally send kind as
	// well, and an explicit task kind still rejects a bug object below.
	if validated.Kind == nil && current.Kind == defaultTaskKind && bugMutation {
		kind = bugKind
	}
	if kind == bugKind && current.Kind == defaultTaskKind && !bugMutation {
		return Task{}, invalid("bug details are required when changing kind to bug", nil)
	}
	if kind == defaultTaskKind && current.Kind == bugKind {
		return Task{}, invalid("a bug cannot be changed back to a task", nil)
	}
	if kind == defaultTaskKind && bugMutation {
		return Task{}, invalid("bug details require kind bug", nil)
	}
	if kind == bugKind && bugMutation && validated.Bug == nil {
		return Task{}, invalid("bug details cannot be null for a bug", nil)
	}
	if current.Kind == defaultTaskKind && kind == bugKind {
		if err := validateBugCreateInput(*validated.Bug); err != nil {
			return Task{}, err
		}
		if strings.TrimSpace(actorID) == "" {
			return Task{}, invalid("a reporter actor is required for bugs", nil)
		}
		if _, err := s.GetActor(ctx, actorID); err != nil {
			return Task{}, invalid("reporter actor not found", nil)
		}
	}
	title, description, priority, columnID, position := current.Title, current.Description, current.Priority, current.ColumnID, current.Position
	assignee := ""
	if current.Assignee != nil {
		assignee = *current.Assignee
	}
	dueAt := ""
	if current.DueAt != nil {
		dueAt = *current.DueAt
	}
	if validated.Title != nil {
		title = *validated.Title
	}
	if validated.Description != nil {
		description = *validated.Description
	}
	if validated.Priority != nil {
		priority = *validated.Priority
	}
	if validated.ColumnID != nil {
		columnID = *validated.ColumnID
	}
	if validated.Position != nil {
		position = *validated.Position
	}
	if validated.AssigneeSet {
		assignee = ""
		if validated.Assignee != nil {
			assignee = strings.TrimSpace(*validated.Assignee)
		}
	}
	if validated.DueAtSet {
		dueAt = ""
		if validated.DueAt != nil {
			dueAt = *validated.DueAt
		}
	}
	column, err := s.GetColumn(ctx, columnID)
	if err != nil {
		return Task{}, err
	}
	// Conversion changes the existing task and ticket in place. Only an
	// implicit backlog destination participates in bug intake; active work
	// and explicitly selected columns retain their existing lifecycle.
	autoBugIntake := current.Kind == defaultTaskKind && kind == bugKind && validated.ColumnID == nil && column.SemanticState == "backlog"
	if autoBugIntake {
		column, err = s.backlogIntakeColumn(ctx, current.ProjectID, bugKind)
		if err != nil {
			return Task{}, err
		}
		columnID = column.ID
	}
	if column.ProjectID != current.ProjectID {
		return Task{}, invalid("column belongs to another project", nil)
	}
	if column.ArchivedAt != nil {
		return Task{}, invalid("column is archived", nil)
	}
	sourceColumn := column
	if current.ColumnID != column.ID {
		sourceColumn, err = s.GetColumn(ctx, current.ColumnID)
		if err != nil {
			return Task{}, err
		}
	}
	if sourceColumn.ArchivedAt != nil {
		return Task{}, invalid("task is assigned to an archived column", nil)
	}
	if kind == bugKind && column.SemanticState == "completed" && (current.Bug == nil || current.Bug.Resolution == nil) {
		return Task{}, invalid("bugs must be resolved before entering a completed column", nil)
	}
	if kind == bugKind && current.Bug != nil && current.Bug.Resolution != nil && column.SemanticState != "completed" {
		return Task{}, invalid("resolved bugs must be reopened before leaving a completed column", nil)
	}
	if assignee != "" {
		if _, err := s.GetActor(ctx, assignee); err != nil {
			return Task{}, invalid("assignee actor not found", nil)
		}
	}
	updated := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		checklistStatus := checklistCompletionStatus{}
		// Derive completion from the authoritative column rows inside the write.
		// The target column can be reclassified by an administrator between the
		// preflight read above and this transaction; using its current semantic
		// state keeps both completed_at and the checklist policy decision aligned.
		// A board can hold only the first cursor page in memory. When a task
		// changes columns without an explicit insertion position, calculate the
		// authoritative destination tail inside the guarded writer statement.
		// Keeping this in the UPDATE avoids retaining a stale read snapshot while
		// waiting for another SQLite writer.
		appendToDestination := current.ColumnID != columnID && validated.Position == nil
		if releaseChanged {
			if err := validateTaskReleaseTx(ctx, tx, current.ProjectID, releaseID, false); err != nil {
				return err
			}
		}
		query := `UPDATE tasks SET kind=?, title=?, description=?, priority=?, column_id=?, position=CASE WHEN ? THEN (SELECT COALESCE(MAX(candidate.position)+1, 0) FROM tasks candidate WHERE candidate.column_id=? AND candidate.deleted_at IS NULL AND candidate.id<>?) ELSE ? END, assignee_id=NULLIF(?, ''), due_at=NULLIF(?, ''), parent_task_id=NULLIF(?, ''), release_id=NULLIF(?, ''), version=version+1, completed_at=CASE WHEN (SELECT semantic_state FROM columns WHERE id=?) = 'completed' THEN CASE WHEN (SELECT semantic_state FROM columns WHERE id=tasks.column_id) = 'completed' THEN tasks.completed_at ELSE ? END ELSE NULL END, updated_at=? WHERE id=? AND version=? AND deleted_at IS NULL`
		args := []any{kind, title, description, priority, columnID, appendToDestination, columnID, id, position, assignee, dueAt, parentID, releaseID, columnID, updated, updated, id, expected}
		if !allowClaimOverride {
			query += ` AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at) <= julianday(?) OR claimed_by=?)`
			args = append(args, updated, actorID)
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			mapped := mapReleaseMutationError(ctx, tx, err, current.ProjectID, id)
			mapped = mapDependencyLifecycleError(ctx, tx, mapped, dependencyLifecycleTarget{TaskID: id})
			child := hierarchyTask{ID: current.ID, ProjectID: current.ProjectID, Number: current.Number, Key: current.Key}
			parentTask := hierarchyTask{}
			if parent != nil {
				parentTask = *parent
			}
			return mapHierarchyMutationError(mapped, child, parentTask)
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			if !allowClaimOverride {
				claimed, details, claimErr := activeClaimByOtherTx(ctx, tx, id, actorID, updated)
				if claimErr != nil {
					return claimErr
				}
				if claimed {
					return &Error{Kind: ErrClaimUnavailable, Message: "task is currently claimed by another actor", Details: details}
				}
			}
			return conflict("task has changed", map[string]any{"current": current})
		}
		destinationState, err := authoritativeColumnStateTx(ctx, tx, columnID, current.ProjectID)
		if err != nil {
			return err
		}
		sourceState := destinationState
		if current.ColumnID != columnID {
			sourceState, err = authoritativeColumnStateTx(ctx, tx, current.ColumnID, current.ProjectID)
			if err != nil {
				return err
			}
		}
		if autoBugIntake && (sourceState != "backlog" || destinationState != "backlog") {
			return conflict("bug intake column changed semantic state", map[string]any{"column_id": columnID})
		}
		if err := validateTaskBugLifecycleTx(ctx, tx, id, kind, destinationState); err != nil {
			return err
		}
		beforeDependency := dependencyTaskFromTask(current, sourceState)
		completionTransition := sourceState != "completed" && destinationState == "completed"
		// Evaluate the policy only after the guarded task update has acquired the
		// SQLite writer transaction. A rejection then rolls back the task version,
		// column, and all activity atomically. Keep this ordering when merging the
		// admin column semantics work so direct task moves cannot bypass TC-10.
		if completionTransition {
			checklistStatus, err = checklistCompletionStatusForTaskTx(ctx, tx, current.ProjectID, current.ID)
			if err != nil {
				return err
			}
			if err := rejectIncompleteChecklist(checklistStatus); err != nil {
				return err
			}
		}
		if validated.LabelsSet {
			if err := replaceTaskLabels(ctx, tx, id, current.ProjectID, validated.Labels); err != nil {
				return err
			}
		}
		if current.Kind == defaultTaskKind && kind == bugKind {
			if err := insertBugDetailsTx(ctx, tx, id, actorID, *validated.Bug); err != nil {
				return err
			}
		} else if kind == bugKind && bugMutation {
			if err := updateBugDetailsTx(ctx, tx, id, *validated.Bug); err != nil {
				return err
			}
		}
		taskMutation := validated.Title != nil || validated.Description != nil || validated.Priority != nil || validated.ColumnID != nil || validated.Position != nil || validated.AssigneeSet || validated.DueAtSet || validated.LabelsSet || kind != current.Kind
		if taskMutation {
			eventPayload := map[string]any{"version": expected + 1}
			// Keep assignment transitions in the event payload so the additive
			// notification read model can fan out without guessing from a later
			// task snapshot. Preserve the historic payload for unrelated patches.
			if validated.AssigneeSet {
				assignmentChanged := assignee != nullableStringValue(current.Assignee)
				eventPayload["assignee"] = assignee
				eventPayload["previous_assignee"] = nullableStringValue(current.Assignee)
				eventPayload["assignment_changed"] = assignmentChanged
			}
			if completionTransition {
				addChecklistCompletionEventFields(eventPayload, checklistStatus)
			}
			if _, err = insertEvent(ctx, tx, "task.updated", actorID, current.ProjectID, id, eventPayload); err != nil {
				return err
			}
		}
		if kind == bugKind && (bugMutation || current.Kind != kind) {
			if _, err = insertEvent(ctx, tx, "bug.updated", actorID, current.ProjectID, id, map[string]any{"version": expected + 1}); err != nil {
				return err
			}
		}
		if parentChanged {
			payload := map[string]any{
				"child_id": id,
				"version":  expected + 1,
			}
			if parent != nil {
				payload["parent_id"] = parent.ID
				payload["parent_key"] = parent.Key
			}
			if previous := nullableStringValue(current.ParentTaskID); previous != "" {
				payload["previous_parent_id"] = previous
			}
			eventType := "task.parent_unlinked"
			if parent != nil {
				eventType = "task.parent_linked"
			}
			if _, err = insertEvent(ctx, tx, eventType, actorID, current.ProjectID, id, payload); err != nil {
				return err
			}
		}
		if releaseChanged {
			previousReleaseID := nullableStringValue(current.ReleaseID)
			var newReleaseValue, previousReleaseValue any
			if releaseID != "" {
				newReleaseValue = releaseID
			}
			if previousReleaseID != "" {
				previousReleaseValue = previousReleaseID
			}
			payload := map[string]any{
				"release_id":          newReleaseValue,
				"previous_release_id": previousReleaseValue,
				"new_release_id":      newReleaseValue,
				"old_release_id":      previousReleaseValue,
				"version":             expected + 1,
			}
			var releaseName, previousReleaseName any
			if releaseID != "" {
				if name, nameErr := releaseNameTx(ctx, tx, releaseID); nameErr == nil {
					releaseName = name
				}
			}
			if previousReleaseID != "" {
				if name, nameErr := releaseNameTx(ctx, tx, previousReleaseID); nameErr == nil {
					previousReleaseName = name
				}
			}
			payload["release_name"] = releaseName
			payload["previous_release_name"] = previousReleaseName
			payload["new_release_name"] = releaseName
			payload["old_release_name"] = previousReleaseName
			if _, err = insertEvent(ctx, tx, "task.release_changed", actorID, current.ProjectID, id, payload); err != nil {
				return err
			}
		}
		// An empty patch or same-value kind/parent-only patch still advances the
		// task version and updated_at in the guarded UPDATE above. Preserve the
		// existing event cardinality for semantic task, bug, and parent updates,
		// but record one fallback task event when none of those paths emitted an
		// event so collection cursors observe the visible write.
		if !taskMutation && !bugMutation && !parentChanged && !releaseChanged {
			if _, err = insertEvent(ctx, tx, "task.updated", actorID, current.ProjectID, id, map[string]any{"version": expected + 1}); err != nil {
				return err
			}
		}
		change, stateChanged, err := dependencyTaskStateChange(ctx, tx, beforeDependency)
		if err != nil {
			return err
		}
		if stateChanged {
			return emitDependencyStateChanges(ctx, tx, actorID, []dependencyStateChange{change})
		}
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}
