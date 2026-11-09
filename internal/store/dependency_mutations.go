package store

import (
	"context"
)

func (s *Store) addTaskDependency(ctx context.Context, dependentReference, prerequisiteReference string, expectedVersion int64, actorID string, allowClaimOverride bool) error {
	return s.withImmediateDependencyTx(ctx, func(q dependencySQL) error {
		dependent, err := resolveDependencyTask(ctx, q, dependentReference)
		if err != nil {
			return err
		}
		if !allowClaimOverride {
			if err := dependencyClaimConflict(ctx, q, dependent.ID, actorID); err != nil {
				return err
			}
		}
		prerequisite, err := resolveDependencyTask(ctx, q, prerequisiteReference)
		if err != nil {
			return err
		}
		if dependent.ID == prerequisite.ID {
			return dependencyInvalid(ErrDependencySelfReference, "a task cannot depend on itself", map[string]any{
				"task": dependencyTaskDetails(dependent),
			}, ErrInvalid)
		}
		if dependent.ProjectID != prerequisite.ProjectID {
			return dependencyInvalid(ErrDependencyCrossProject, "dependencies must stay within one project", map[string]any{
				"task_project_id":         dependent.ProjectID,
				"prerequisite_project_id": prerequisite.ProjectID,
				"task_id":                 dependent.ID,
				"prerequisite_task_id":    prerequisite.ID,
			}, ErrInvalid)
		}
		if dependent.Version != expectedVersion {
			return conflict("task has changed", map[string]any{
				"current_version":  dependent.Version,
				"expected_version": expectedVersion,
				"current": map[string]any{
					"id":      dependent.ID,
					"key":     dependent.Key,
					"version": dependent.Version,
				},
			})
		}

		var edgeExists int
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_dependencies WHERE task_id=? AND prerequisite_task_id=?)`, dependent.ID, prerequisite.ID).Scan(&edgeExists); err != nil {
			return err
		}
		if edgeExists != 0 {
			return dependencyInvalid(ErrDependencyAlreadyExists, "dependency already exists", map[string]any{
				"task":         dependencyTaskDetails(dependent),
				"prerequisite": dependencyTaskDetails(prerequisite),
			}, ErrAlreadyExists)
		}

		var prerequisiteCount, dependentCount int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(1)
			FROM task_dependencies td
			JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
			WHERE td.task_id=? AND prerequisite.project_id=? AND prerequisite.deleted_at IS NULL`, dependent.ID, dependent.ProjectID).Scan(&prerequisiteCount); err != nil {
			return err
		}
		if prerequisiteCount >= maxDirectTaskDependencies {
			return dependencyInvalid(ErrDependencyLimitExceeded, "task has reached its direct prerequisite limit", map[string]any{
				"direction": "prerequisites",
				"limit":     maxDirectTaskDependencies,
				"count":     prerequisiteCount,
				"task_id":   dependent.ID,
			}, ErrInvalid)
		}
		if err := q.QueryRowContext(ctx, `SELECT COUNT(1)
			FROM task_dependencies td
			JOIN tasks dependent_task ON dependent_task.id=td.task_id
			WHERE td.prerequisite_task_id=? AND dependent_task.project_id=? AND dependent_task.deleted_at IS NULL`, prerequisite.ID, prerequisite.ProjectID).Scan(&dependentCount); err != nil {
			return err
		}
		if dependentCount >= maxDirectTaskDependencies {
			return dependencyInvalid(ErrDependencyLimitExceeded, "task has reached its direct dependent limit", map[string]any{
				"direction": "dependents",
				"limit":     maxDirectTaskDependencies,
				"count":     dependentCount,
				"task_id":   prerequisite.ID,
			}, ErrInvalid)
		}

		var reachable int
		if err := q.QueryRowContext(ctx, `WITH RECURSIVE prerequisite_graph(task_id) AS (
			SELECT prerequisite_task_id FROM task_dependencies WHERE task_id=?
			UNION
			SELECT td.prerequisite_task_id
			FROM task_dependencies td
			JOIN prerequisite_graph graph ON graph.task_id=td.task_id
		)
		SELECT EXISTS(SELECT 1 FROM prerequisite_graph WHERE task_id=?)`, prerequisite.ID, dependent.ID).Scan(&reachable); err != nil {
			return err
		}
		if reachable != 0 {
			details, err := dependencyCycleDetails(ctx, q, prerequisite, dependent)
			if err != nil {
				return err
			}
			return dependencyInvalid(ErrDependencyCycle, "dependency would create a cycle", details, ErrConflict)
		}

		createdAt := now()
		if _, err := q.ExecContext(ctx, `INSERT INTO task_dependencies(task_id, prerequisite_task_id, created_by, created_at) VALUES (?, ?, NULLIF(?, ''), ?)`, dependent.ID, prerequisite.ID, actorID, createdAt); err != nil {
			return mapDependencyMutationError(ctx, q, err, dependent, prerequisite)
		}
		result, err := q.ExecContext(ctx, `UPDATE tasks SET version=version+1, updated_at=? WHERE id=? AND version=? AND deleted_at IS NULL`, createdAt, dependent.ID, expectedVersion)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return conflict("task has changed", map[string]any{
				"current_version":  dependent.Version,
				"expected_version": expectedVersion,
			})
		}
		return insertDependencyEvent(ctx, q, "task.dependency_added", actorID, dependent, prerequisite, dependent.Version+1)
	})
}

func (s *Store) removeTaskDependency(ctx context.Context, dependentReference, prerequisiteReference string, expectedVersion int64, actorID string, allowClaimOverride bool) error {
	return s.withImmediateDependencyTx(ctx, func(q dependencySQL) error {
		dependent, err := resolveDependencyTask(ctx, q, dependentReference)
		if err != nil {
			return err
		}
		if !allowClaimOverride {
			if err := dependencyClaimConflict(ctx, q, dependent.ID, actorID); err != nil {
				return err
			}
		}
		prerequisite, err := resolveDependencyTask(ctx, q, prerequisiteReference)
		if err != nil {
			return err
		}
		if dependent.ID == prerequisite.ID {
			return dependencyInvalid(ErrDependencySelfReference, "a task cannot depend on itself", map[string]any{
				"task": dependencyTaskDetails(dependent),
			}, ErrInvalid)
		}
		if dependent.ProjectID != prerequisite.ProjectID {
			return dependencyInvalid(ErrDependencyCrossProject, "dependencies must stay within one project", map[string]any{
				"task_project_id":         dependent.ProjectID,
				"prerequisite_project_id": prerequisite.ProjectID,
				"task_id":                 dependent.ID,
				"prerequisite_task_id":    prerequisite.ID,
			}, ErrInvalid)
		}
		if dependent.Version != expectedVersion {
			return conflict("task has changed", map[string]any{
				"current_version":  dependent.Version,
				"expected_version": expectedVersion,
			})
		}

		var edgeExists int
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_dependencies WHERE task_id=? AND prerequisite_task_id=?)`, dependent.ID, prerequisite.ID).Scan(&edgeExists); err != nil {
			return err
		}
		if edgeExists == 0 {
			return dependencyNotFound("dependency not found", map[string]any{
				"task_id":              dependent.ID,
				"prerequisite_task_id": prerequisite.ID,
			})
		}
		createdAt := now()
		query := `DELETE FROM task_dependencies WHERE task_id=? AND prerequisite_task_id=?`
		result, err := q.ExecContext(ctx, query, dependent.ID, prerequisite.ID)
		if err != nil {
			return err
		}
		removed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if removed != 1 {
			return dependencyNotFound("dependency not found", map[string]any{
				"task_id":              dependent.ID,
				"prerequisite_task_id": prerequisite.ID,
			})
		}
		updateQuery := `UPDATE tasks SET version=version+1, updated_at=? WHERE id=? AND deleted_at IS NULL`
		updateArgs := []any{createdAt, dependent.ID}
		updateQuery += ` AND version=?`
		updateArgs = append(updateArgs, expectedVersion)
		updated, err := q.ExecContext(ctx, updateQuery, updateArgs...)
		if err != nil {
			return err
		}
		changed, err := updated.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return conflict("task has changed", map[string]any{
				"current_version":  dependent.Version,
				"expected_version": expectedVersion,
			})
		}
		return insertDependencyEvent(ctx, q, "task.dependency_removed", actorID, dependent, prerequisite, dependent.Version+1)
	})
}

// AddTaskDependency adds prerequisiteReference as a direct prerequisite of
// dependentReference. The source version is required to preserve the same
// optimistic-concurrency contract as other task writes. The returned task is
// the dependent and only its editable version advances.
func (s *Store) AddTaskDependency(ctx context.Context, dependentReference, prerequisiteReference string, expectedVersion int64, actorID string) (Task, error) {
	return s.addTaskDependencyWithClaimOverride(ctx, dependentReference, prerequisiteReference, expectedVersion, actorID, false)
}

// AddTaskDependencyWithClaimOverride is the administrator variant used by the
// HTTP layer for a human administrator. The claim check remains inside the
// dependency transaction; callers must not pass true based only on a
// preflight read. Bearer identities are never granted this override by the
// HTTP layer.
func (s *Store) AddTaskDependencyWithClaimOverride(ctx context.Context, dependentReference, prerequisiteReference string, expectedVersion int64, actorID string, allowClaimOverride bool) (Task, error) {
	return s.addTaskDependencyWithClaimOverride(ctx, dependentReference, prerequisiteReference, expectedVersion, actorID, allowClaimOverride)
}

func (s *Store) addTaskDependencyWithClaimOverride(ctx context.Context, dependentReference, prerequisiteReference string, expectedVersion int64, actorID string, allowClaimOverride bool) (Task, error) {
	if expectedVersion <= 0 {
		return Task{}, ErrPrecondition
	}
	if err := s.addTaskDependency(ctx, dependentReference, prerequisiteReference, expectedVersion, actorID, allowClaimOverride); err != nil {
		return Task{}, err
	}
	return s.ResolveTaskReference(ctx, dependentReference)
}

// RemoveTaskDependency removes one direct prerequisite edge. The source
// version is required to preserve optimistic concurrency and is incremented
// only on the dependent task.
func (s *Store) RemoveTaskDependency(ctx context.Context, dependentReference, prerequisiteReference string, expectedVersion int64, actorID string) (Task, error) {
	return s.removeTaskDependencyWithClaimOverride(ctx, dependentReference, prerequisiteReference, expectedVersion, actorID, false)
}

// RemoveTaskDependencyWithClaimOverride is the administrator counterpart to
// AddTaskDependencyWithClaimOverride. See that method for the trust boundary.
func (s *Store) RemoveTaskDependencyWithClaimOverride(ctx context.Context, dependentReference, prerequisiteReference string, expectedVersion int64, actorID string, allowClaimOverride bool) (Task, error) {
	return s.removeTaskDependencyWithClaimOverride(ctx, dependentReference, prerequisiteReference, expectedVersion, actorID, allowClaimOverride)
}

func (s *Store) removeTaskDependencyWithClaimOverride(ctx context.Context, dependentReference, prerequisiteReference string, expectedVersion int64, actorID string, allowClaimOverride bool) (Task, error) {
	if expectedVersion <= 0 {
		return Task{}, ErrPrecondition
	}
	if err := s.removeTaskDependency(ctx, dependentReference, prerequisiteReference, expectedVersion, actorID, allowClaimOverride); err != nil {
		return Task{}, err
	}
	return s.ResolveTaskReference(ctx, dependentReference)
}

func insertDependencyEvent(ctx context.Context, q dependencySQL, eventType, actorID string, dependent, prerequisite dependencyTask, version int64) error {
	payload := map[string]any{
		"dependent_id":     dependent.ID,
		"dependent_key":    dependent.Key,
		"prerequisite_id":  prerequisite.ID,
		"prerequisite_key": prerequisite.Key,
		"version":          version,
	}
	_, err := q.ExecContext(ctx, `INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?)`, newID(), eventType, actorID, dependent.ProjectID, dependent.ID, eventPayload(payload), now())
	return err
}
