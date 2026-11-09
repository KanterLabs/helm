package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const maxDirectTaskDependencies = 200

type dependencyTask struct {
	ID            string
	ProjectID     string
	ProjectKey    string
	Number        int
	Key           string
	Title         string
	CompletedAt   sql.NullString
	SemanticState string
	Version       int64
}

// dependencyTaskFromTask adapts the store's enriched task snapshot for
// lifecycle comparisons. The caller must supply the semantic state belonging
// to the snapshot's source column; task responses intentionally keep that
// state on the column rather than duplicating it on Task.
func dependencyTaskFromTask(task Task, semanticState string) dependencyTask {
	var completedAt sql.NullString
	if task.CompletedAt != nil {
		completedAt = sql.NullString{String: *task.CompletedAt, Valid: true}
	}
	return dependencyTask{
		ID:            task.ID,
		ProjectID:     task.ProjectID,
		ProjectKey:    strings.TrimSuffix(task.Key, fmt.Sprintf("-%d", task.Number)),
		Number:        task.Number,
		Key:           task.Key,
		Title:         task.Title,
		CompletedAt:   completedAt,
		SemanticState: semanticState,
		Version:       task.Version,
	}
}

func dependencyNotFound(message string, details any) error {
	return &Error{Kind: errors.Join(ErrDependencyNotFound, ErrNotFound), Message: message, Details: details}
}

func dependencyInvalid(kind error, message string, details any, fallback error) error {
	return &Error{Kind: errors.Join(kind, fallback), Message: message, Details: details}
}

func mapDependencyMutationError(ctx context.Context, q dependencySQL, err error, dependent, prerequisite dependencyTask) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "unmet_dependencies"):
		return dependencyInvalid(ErrUnmetDependencies, "dependency prerequisites are not satisfied for this task state", map[string]any{
			"task":         dependencyTaskDetails(dependent),
			"prerequisite": dependencyTaskDetails(prerequisite),
		}, ErrInvalid)
	case strings.Contains(lower, "dependency_cycle"):
		details, detailsErr := dependencyCycleDetails(ctx, q, prerequisite, dependent)
		if detailsErr == nil {
			return dependencyInvalid(ErrDependencyCycle, "dependency would create a cycle", details, ErrConflict)
		}
		return dependencyInvalid(ErrDependencyCycle, "dependency would create a cycle", map[string]any{
			"task_id":              dependent.ID,
			"prerequisite_task_id": prerequisite.ID,
		}, ErrConflict)
	case strings.Contains(lower, "dependency_limit_exceeded_prerequisites"):
		return dependencyInvalid(ErrDependencyLimitExceeded, "task has reached its direct prerequisite limit", map[string]any{
			"direction": "prerequisites",
			"limit":     maxDirectTaskDependencies,
			"task_id":   dependent.ID,
		}, ErrInvalid)
	case strings.Contains(lower, "dependency_limit_exceeded_dependents"):
		return dependencyInvalid(ErrDependencyLimitExceeded, "task has reached its direct dependent limit", map[string]any{
			"direction": "dependents",
			"limit":     maxDirectTaskDependencies,
			"task_id":   prerequisite.ID,
		}, ErrInvalid)
	case strings.Contains(lower, "dependency_cross_project_or_not_live"):
		return dependencyInvalid(ErrDependencyCrossProject, "dependencies must stay within one live project", map[string]any{
			"task_id":              dependent.ID,
			"prerequisite_task_id": prerequisite.ID,
		}, ErrInvalid)
	case strings.Contains(lower, "unique constraint failed") && strings.Contains(lower, "task_dependencies"):
		return dependencyInvalid(ErrDependencyAlreadyExists, "dependency already exists", map[string]any{
			"task":         dependencyTaskDetails(dependent),
			"prerequisite": dependencyTaskDetails(prerequisite),
		}, ErrAlreadyExists)
	default:
		return err
	}
}

func resolveDependencyTask(ctx context.Context, q dependencySQL, reference string) (dependencyTask, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return dependencyTask{}, dependencyNotFound("task not found", nil)
	}
	var task dependencyTask
	if err := q.QueryRowContext(ctx, `SELECT t.id, t.project_id, p.key, t.number, t.title, t.completed_at, c.semantic_state, t.version
		FROM tasks t
		JOIN projects p ON p.id=t.project_id
		JOIN columns c ON c.id=t.column_id
		WHERE t.deleted_at IS NULL
		  AND (t.id=? OR lower(p.key || '-' || CAST(t.number AS TEXT))=lower(?))
		LIMIT 1`, reference, reference).Scan(&task.ID, &task.ProjectID, &task.ProjectKey, &task.Number, &task.Title, &task.CompletedAt, &task.SemanticState, &task.Version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dependencyTask{}, dependencyNotFound("task not found", map[string]any{"reference": reference})
		}
		return dependencyTask{}, err
	}
	task.Key = fmt.Sprintf("%s-%d", task.ProjectKey, task.Number)
	return task, nil
}

func dependencySatisfied(task dependencyTask) bool {
	return task.CompletedAt.Valid && task.SemanticState == "completed"
}

func dependencyReference(task dependencyTask) TaskReference {
	return TaskReference{
		ID:          task.ID,
		Key:         task.Key,
		Title:       task.Title,
		CompletedAt: nullableString(task.CompletedAt),
		Satisfied:   dependencySatisfied(task),
	}
}

func dependencyTaskDetails(task dependencyTask) map[string]any {
	return map[string]any{
		"id":           task.ID,
		"key":          task.Key,
		"title":        task.Title,
		"completed_at": nullableString(task.CompletedAt),
		"satisfied":    dependencySatisfied(task),
	}
}

func dependencyCyclePath(ctx context.Context, q dependencySQL, prerequisiteID, dependentID string) ([]string, error) {
	// IDs are opaque hex strings and therefore safe in this delimiter-based
	// path representation. The depth bound makes malformed legacy data unable
	// to turn a validation failure into unbounded work.
	var encoded string
	err := q.QueryRowContext(ctx, `WITH RECURSIVE dependency_path(task_id, path, depth) AS (
		SELECT ?, ?, 0
		UNION ALL
		SELECT td.prerequisite_task_id,
		       dependency_path.path || ',' || td.prerequisite_task_id,
		       dependency_path.depth + 1
		FROM task_dependencies td
		JOIN dependency_path ON td.task_id=dependency_path.task_id
		JOIN tasks next_task ON next_task.id=td.prerequisite_task_id AND next_task.deleted_at IS NULL
		WHERE dependency_path.depth < ?
		  AND instr(',' || dependency_path.path || ',', ',' || td.prerequisite_task_id || ',') = 0
	)
	SELECT path FROM dependency_path WHERE task_id=? LIMIT 1`, prerequisiteID, prerequisiteID, maxDirectTaskDependencies, dependentID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{prerequisiteID, dependentID}, nil
	}
	if err != nil {
		return nil, err
	}
	parts := strings.Split(encoded, ",")
	parts = append(parts, prerequisiteID)
	return parts, nil
}

func dependencyCycleDetails(ctx context.Context, q dependencySQL, prerequisite, dependent dependencyTask) (map[string]any, error) {
	pathIDs, err := dependencyCyclePath(ctx, q, prerequisite.ID, dependent.ID)
	if err != nil {
		return nil, err
	}
	pathKeys := make([]string, 0, len(pathIDs))
	for _, id := range pathIDs {
		var projectKey string
		var number int
		if err := q.QueryRowContext(ctx, `SELECT p.key, t.number FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.id=?`, id).Scan(&projectKey, &number); err != nil {
			return nil, err
		}
		pathKeys = append(pathKeys, fmt.Sprintf("%s-%d", projectKey, number))
	}
	return map[string]any{
		"task_id":              dependent.ID,
		"task_key":             dependent.Key,
		"prerequisite_task_id": prerequisite.ID,
		"prerequisite_key":     prerequisite.Key,
		"path":                 pathKeys,
		"path_ids":             pathIDs,
	}, nil
}

func dependencyClaimConflict(ctx context.Context, q dependencySQL, taskID, actorID string) error {
	var claimedBy, claimExpiresAt string
	err := q.QueryRowContext(ctx, `SELECT claimed_by, claim_expires_at
		FROM tasks
		WHERE id=?
		  AND deleted_at IS NULL
		  AND claimed_by IS NOT NULL
		  AND claimed_by<>?
		  AND claim_expires_at IS NOT NULL
		  AND julianday(claim_expires_at) > julianday('now')`, taskID, actorID).Scan(&claimedBy, &claimExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return &Error{
		Kind:    ErrClaimUnavailable,
		Message: "task is currently claimed by another actor",
		Details: map[string]any{"task_id": taskID, "claimed_by": claimedBy, "claim_expires_at": claimExpiresAt},
	}
}
