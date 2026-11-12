package store

import (
	"context"
	"fmt"
	"strings"
)

// dependencyLifecycleTarget identifies the mutation boundary that raised a
// database dependency guard. Task transitions pass TaskID; a semantic column
// update passes ColumnID because one statement may have affected many tasks.
// Keeping this target unexported prevents callers from bypassing the guarded
// lifecycle APIs with a hand-built detail shape.
type dependencyLifecycleTarget struct {
	TaskID            string
	ColumnID          string
	AllDependents     bool
	NextSemanticState string
}

// mapDependencyLifecycleError translates the fail-closed migration guards into
// stable store errors while retaining bounded, actionable relation details.
// The original trigger error is never allowed to replace the typed sentinel
// if a detail query fails; callers still receive a machine-readable conflict.
func mapDependencyLifecycleError(ctx context.Context, q dependencySQL, err error, target dependencyLifecycleTarget) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "unmet_dependencies"):
		details, detailsErr := dependencyUnmetLifecycleDetails(ctx, q, target)
		if detailsErr != nil {
			details = map[string]any{"task_id": target.TaskID, "column_id": target.ColumnID}
		}
		return dependencyInvalid(ErrUnmetDependencies, "task prerequisites are not satisfied", details, ErrConflict)
	case strings.Contains(lower, "dependency_in_use"):
		details, detailsErr := dependencyInUseLifecycleDetails(ctx, q, target)
		if detailsErr != nil {
			details = map[string]any{"task_id": target.TaskID, "column_id": target.ColumnID}
		}
		return dependencyInvalid(ErrDependencyInUse, "dependency change would invalidate live dependent work", details, ErrConflict)
	default:
		return err
	}
}

func dependencyUnmetLifecycleDetails(ctx context.Context, q dependencySQL, target dependencyLifecycleTarget) (map[string]any, error) {
	if target.TaskID != "" {
		task, err := resolveDependencyTask(ctx, q, target.TaskID)
		if err != nil {
			return nil, err
		}
		prerequisites, err := dependencyUnmetPrerequisites(ctx, q, task)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"task_id":             task.ID,
			"task_key":            task.Key,
			"unmet_prerequisites": prerequisites,
		}, nil
	}
	if target.ColumnID == "" {
		return map[string]any{"unmet_prerequisites": []TaskReference{}}, nil
	}
	return dependencyUnmetColumnDetails(ctx, q, target.ColumnID)
}

func dependencyUnmetPrerequisites(ctx context.Context, q dependencySQL, task dependencyTask) ([]TaskReference, error) {
	rows, err := q.QueryContext(ctx, `SELECT prerequisite.id, prerequisite.number, prerequisite.title, prerequisite.completed_at, prerequisite_column.semantic_state, prerequisite_project.key
		FROM task_dependencies td
		JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
		JOIN projects prerequisite_project ON prerequisite_project.id=prerequisite.project_id
		JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
		WHERE td.task_id=?
		  AND prerequisite.project_id=?
		  AND prerequisite.deleted_at IS NULL
		  AND (prerequisite.completed_at IS NULL OR prerequisite_column.semantic_state <> 'completed')
		ORDER BY prerequisite.number, prerequisite.id
		LIMIT ?`, task.ID, task.ProjectID, maxDirectTaskDependencies)
	if err != nil {
		return nil, err
	}
	result := make([]TaskReference, 0)
	for rows.Next() {
		var prerequisite dependencyTask
		if err := rows.Scan(&prerequisite.ID, &prerequisite.Number, &prerequisite.Title, &prerequisite.CompletedAt, &prerequisite.SemanticState, &prerequisite.ProjectKey); err != nil {
			rows.Close()
			return nil, err
		}
		prerequisite.ProjectID = task.ProjectID
		prerequisite.Key = fmt.Sprintf("%s-%d", prerequisite.ProjectKey, prerequisite.Number)
		result = append(result, dependencyReference(prerequisite))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return result, nil
}

func dependencyUnmetColumnDetails(ctx context.Context, q dependencySQL, columnID string) (map[string]any, error) {
	rows, err := q.QueryContext(ctx, `SELECT dependent.id, dependent.number, dependent.title, dependent_project.key,
		prerequisite.id, prerequisite.number, prerequisite.title, prerequisite.completed_at, prerequisite_column.semantic_state, prerequisite_project.key
		FROM tasks dependent
		JOIN projects dependent_project ON dependent_project.id=dependent.project_id
		JOIN task_dependencies td ON td.task_id=dependent.id
		JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
		JOIN projects prerequisite_project ON prerequisite_project.id=prerequisite.project_id
		JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
		WHERE dependent.column_id=?
		  AND dependent.deleted_at IS NULL
		  AND prerequisite.deleted_at IS NULL
		  AND prerequisite.project_id=dependent.project_id
		  AND (prerequisite.completed_at IS NULL OR prerequisite_column.semantic_state <> 'completed')
		ORDER BY dependent.number, dependent.id, prerequisite.number, prerequisite.id
		LIMIT ?`, columnID, maxDirectTaskDependencies)
	if err != nil {
		return nil, err
	}
	type blockedTask struct {
		ID            string
		Key           string
		Title         string
		Prerequisites []TaskReference
	}
	ordered := make([]blockedTask, 0)
	byID := make(map[string]int)
	for rows.Next() {
		var dependent dependencyTask
		var prerequisite dependencyTask
		if err := rows.Scan(&dependent.ID, &dependent.Number, &dependent.Title, &dependent.ProjectKey, &prerequisite.ID, &prerequisite.Number, &prerequisite.Title, &prerequisite.CompletedAt, &prerequisite.SemanticState, &prerequisite.ProjectKey); err != nil {
			rows.Close()
			return nil, err
		}
		dependent.Key = fmt.Sprintf("%s-%d", dependent.ProjectKey, dependent.Number)
		prerequisite.Key = fmt.Sprintf("%s-%d", prerequisite.ProjectKey, prerequisite.Number)
		index, ok := byID[dependent.ID]
		if !ok {
			index = len(ordered)
			byID[dependent.ID] = index
			ordered = append(ordered, blockedTask{ID: dependent.ID, Key: dependent.Key, Title: dependent.Title, Prerequisites: make([]TaskReference, 0)})
		}
		ordered[index].Prerequisites = append(ordered[index].Prerequisites, dependencyReference(prerequisite))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	tasks := make([]map[string]any, 0, len(ordered))
	for _, task := range ordered {
		tasks = append(tasks, map[string]any{
			"task_id":             task.ID,
			"task_key":            task.Key,
			"task_title":          task.Title,
			"unmet_prerequisites": task.Prerequisites,
		})
	}
	return map[string]any{"column_id": columnID, "tasks": tasks}, nil
}

func dependencyInUseLifecycleDetails(ctx context.Context, q dependencySQL, target dependencyLifecycleTarget) (map[string]any, error) {
	if target.TaskID != "" {
		task, err := resolveDependencyTask(ctx, q, target.TaskID)
		if err != nil {
			return nil, err
		}
		dependents, err := dependencyLiveDependents(ctx, q, task, target.AllDependents)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"task_id":    task.ID,
			"task_key":   task.Key,
			"dependents": dependents,
		}, nil
	}
	if target.ColumnID == "" {
		return map[string]any{"dependents": []TaskReference{}}, nil
	}
	return dependencyInUseColumnDetails(ctx, q, target.ColumnID, target.NextSemanticState)
}

func dependencyLiveDependents(ctx context.Context, q dependencySQL, prerequisite dependencyTask, includeAll bool) ([]TaskReference, error) {
	query := `SELECT dependent.id, dependent.number, dependent.title, dependent.completed_at, dependent_column.semantic_state, dependent_project.key
		FROM task_dependencies td
		JOIN tasks dependent ON dependent.id=td.task_id
		JOIN projects dependent_project ON dependent_project.id=dependent.project_id
		JOIN columns dependent_column ON dependent_column.id=dependent.column_id
		WHERE td.prerequisite_task_id=?
		  AND dependent.project_id=?
		  AND dependent.deleted_at IS NULL`
	if !includeAll {
		query += `
		  AND (
		      dependent_column.semantic_state='active'
		      OR (
		          dependent.claimed_by IS NOT NULL
		          AND dependent.claim_expires_at IS NOT NULL
		          AND julianday(dependent.claim_expires_at) > julianday('now')
		      )
		  )
		  AND (dependent_column.semantic_state <> 'completed' OR dependent.completed_at IS NULL)`
	}
	query += `
		ORDER BY dependent.number, dependent.id
		LIMIT ?`
	rows, err := q.QueryContext(ctx, query, prerequisite.ID, prerequisite.ProjectID, maxDirectTaskDependencies)
	if err != nil {
		return nil, err
	}
	result := make([]TaskReference, 0)
	for rows.Next() {
		var dependent dependencyTask
		if err := rows.Scan(&dependent.ID, &dependent.Number, &dependent.Title, &dependent.CompletedAt, &dependent.SemanticState, &dependent.ProjectKey); err != nil {
			rows.Close()
			return nil, err
		}
		dependent.ProjectID = prerequisite.ProjectID
		dependent.Key = fmt.Sprintf("%s-%d", dependent.ProjectKey, dependent.Number)
		result = append(result, dependencyReference(dependent))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return result, nil
}

func dependencyInUseColumnDetails(ctx context.Context, q dependencySQL, columnID, nextSemanticState string) (map[string]any, error) {
	rows, err := q.QueryContext(ctx, `SELECT prerequisite.id, prerequisite.number, prerequisite.title, prerequisite_project.key,
		dependent.id, dependent.number, dependent.title, dependent.completed_at, dependent_column.semantic_state, dependent_project.key
		FROM tasks prerequisite
		JOIN projects prerequisite_project ON prerequisite_project.id=prerequisite.project_id
		JOIN task_dependencies td ON td.prerequisite_task_id=prerequisite.id
		JOIN tasks dependent ON dependent.id=td.task_id
		JOIN projects dependent_project ON dependent_project.id=dependent.project_id
		JOIN columns dependent_column ON dependent_column.id=dependent.column_id
		WHERE prerequisite.column_id=?
		  AND prerequisite.deleted_at IS NULL
		  AND dependent.deleted_at IS NULL
		  AND dependent.project_id=prerequisite.project_id
		  AND (
		      (CASE WHEN dependent.column_id=? THEN ? ELSE COALESCE(dependent_column.semantic_state, '') END)='active'
		      OR (
		          dependent.claimed_by IS NOT NULL
		          AND dependent.claim_expires_at IS NOT NULL
		          AND julianday(dependent.claim_expires_at) > julianday('now')
		      )
		  )
		  AND (
		      dependent.column_id=?
		      OR dependent.completed_at IS NULL
		      OR COALESCE(dependent_column.semantic_state, '') <> 'completed'
		  )
		ORDER BY prerequisite.number, prerequisite.id, dependent.number, dependent.id
		LIMIT ?`, columnID, columnID, nextSemanticState, columnID, maxDirectTaskDependencies)
	if err != nil {
		return nil, err
	}
	type blockingTask struct {
		ID         string
		Key        string
		Title      string
		Dependents []TaskReference
	}
	ordered := make([]blockingTask, 0)
	byID := make(map[string]int)
	for rows.Next() {
		var prerequisite dependencyTask
		var dependent dependencyTask
		if err := rows.Scan(&prerequisite.ID, &prerequisite.Number, &prerequisite.Title, &prerequisite.ProjectKey, &dependent.ID, &dependent.Number, &dependent.Title, &dependent.CompletedAt, &dependent.SemanticState, &dependent.ProjectKey); err != nil {
			rows.Close()
			return nil, err
		}
		prerequisite.Key = fmt.Sprintf("%s-%d", prerequisite.ProjectKey, prerequisite.Number)
		dependent.Key = fmt.Sprintf("%s-%d", dependent.ProjectKey, dependent.Number)
		index, ok := byID[prerequisite.ID]
		if !ok {
			index = len(ordered)
			byID[prerequisite.ID] = index
			ordered = append(ordered, blockingTask{ID: prerequisite.ID, Key: prerequisite.Key, Title: prerequisite.Title, Dependents: make([]TaskReference, 0)})
		}
		ordered[index].Dependents = append(ordered[index].Dependents, dependencyReference(dependent))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	tasks := make([]map[string]any, 0, len(ordered))
	for _, task := range ordered {
		tasks = append(tasks, map[string]any{
			"task_id":    task.ID,
			"task_key":   task.Key,
			"task_title": task.Title,
			"dependents": task.Dependents,
		})
	}
	return map[string]any{"column_id": columnID, "tasks": tasks}, nil
}
