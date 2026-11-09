package store

import (
	"context"
	"fmt"
	"strings"
)

// GetTaskDependencies returns direct live relations in both directions. The
// source and every relation are soft-delete safe; dangling edges left by a
// retained pre-cleanup writer are intentionally invisible.
func (s *Store) GetTaskDependencies(ctx context.Context, reference string) (TaskDependencies, error) {
	return s.listTaskDependencies(ctx, reference)
}

func (s *Store) listTaskDependencies(ctx context.Context, reference string) (TaskDependencies, error) {
	task, err := resolveDependencyTask(ctx, s.DB, reference)
	if err != nil {
		return TaskDependencies{}, err
	}
	result := TaskDependencies{
		Prerequisites: make([]TaskReference, 0),
		Dependents:    make([]TaskReference, 0),
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT prerequisite.id, prerequisite.number, prerequisite.title, prerequisite.completed_at, prerequisite_column.semantic_state, prerequisite_project.key
		FROM task_dependencies td
		JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
		JOIN projects prerequisite_project ON prerequisite_project.id=prerequisite.project_id
		JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
		WHERE td.task_id=?
		  AND prerequisite.project_id=?
		  AND prerequisite.deleted_at IS NULL
		ORDER BY prerequisite.number, prerequisite.id
		LIMIT ?`, task.ID, task.ProjectID, maxDirectTaskDependencies)
	if err != nil {
		return TaskDependencies{}, err
	}
	for rows.Next() {
		var relation dependencyTask
		if err := rows.Scan(&relation.ID, &relation.Number, &relation.Title, &relation.CompletedAt, &relation.SemanticState, &relation.ProjectKey); err != nil {
			rows.Close()
			return TaskDependencies{}, err
		}
		relation.ProjectID = task.ProjectID
		relation.Key = fmt.Sprintf("%s-%d", relation.ProjectKey, relation.Number)
		result.Prerequisites = append(result.Prerequisites, dependencyReference(relation))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return TaskDependencies{}, err
	}
	rows.Close()

	rows, err = s.DB.QueryContext(ctx, `SELECT dependent.id, dependent.number, dependent.title, dependent.completed_at, dependent_column.semantic_state, dependent_project.key
		FROM task_dependencies td
		JOIN tasks dependent ON dependent.id=td.task_id
		JOIN projects dependent_project ON dependent_project.id=dependent.project_id
		JOIN columns dependent_column ON dependent_column.id=dependent.column_id
		WHERE td.prerequisite_task_id=?
		  AND dependent.project_id=?
		  AND dependent.deleted_at IS NULL
		ORDER BY dependent.number, dependent.id
		LIMIT ?`, task.ID, task.ProjectID, maxDirectTaskDependencies)
	if err != nil {
		return TaskDependencies{}, err
	}
	for rows.Next() {
		var relation dependencyTask
		if err := rows.Scan(&relation.ID, &relation.Number, &relation.Title, &relation.CompletedAt, &relation.SemanticState, &relation.ProjectKey); err != nil {
			rows.Close()
			return TaskDependencies{}, err
		}
		relation.ProjectID = task.ProjectID
		relation.Key = fmt.Sprintf("%s-%d", relation.ProjectKey, relation.Number)
		result.Dependents = append(result.Dependents, dependencyReference(relation))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return TaskDependencies{}, err
	}
	rows.Close()
	return result, nil
}

// populateTaskDependencySummaries fills summaries for a collection with one
// grouped query. Callers should invoke it after scanning their task page and
// before returning the page; an empty slice is a no-op. The source task and
// both relation directions are filtered for live, same-project rows.
func (s *Store) populateTaskDependencySummaries(ctx context.Context, tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]string, 0, len(tasks))
	seen := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task.ID == "" {
			continue
		}
		if _, ok := seen[task.ID]; ok {
			continue
		}
		seen[task.ID] = struct{}{}
		ids = append(ids, task.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	for i := range tasks {
		tasks[i].DependencySummary = DependencySummary{}
	}
	placeholders := make([]string, len(ids))
	for i := range ids {
		placeholders[i] = "?"
	}
	// The candidate list is repeated for each aggregate and the final task
	// selection. This keeps the grouped work bounded to the requested page
	// rather than scanning all historical graph rows.
	args := make([]any, 0, len(ids)*3)
	for repeat := 0; repeat < 3; repeat++ {
		for _, id := range ids {
			args = append(args, id)
		}
	}
	query := `WITH direct_prerequisites AS (
		SELECT td.task_id,
		       COUNT(1) AS prerequisite_count,
		       SUM(CASE WHEN prerequisite.completed_at IS NULL OR prerequisite_column.semantic_state <> 'completed' THEN 1 ELSE 0 END) AS unmet_prerequisite_count
		FROM task_dependencies td
		JOIN tasks source ON source.id=td.task_id AND source.deleted_at IS NULL
		JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
		JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
		WHERE td.task_id IN (` + strings.Join(placeholders, ",") + `)
		  AND prerequisite.deleted_at IS NULL
		  AND prerequisite.project_id=source.project_id
		GROUP BY td.task_id
	), direct_dependents AS (
		SELECT td.prerequisite_task_id,
		       COUNT(1) AS dependent_count
		FROM task_dependencies td
		JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id AND prerequisite.deleted_at IS NULL
		JOIN tasks dependent ON dependent.id=td.task_id
		WHERE td.prerequisite_task_id IN (` + strings.Join(placeholders, ",") + `)
		  AND dependent.deleted_at IS NULL
		  AND dependent.project_id=prerequisite.project_id
		GROUP BY td.prerequisite_task_id
	)
	SELECT task.id,
	       COALESCE(direct_prerequisites.prerequisite_count, 0),
	       COALESCE(direct_prerequisites.unmet_prerequisite_count, 0),
	       COALESCE(direct_dependents.dependent_count, 0)
	FROM tasks task
	LEFT JOIN direct_prerequisites ON direct_prerequisites.task_id=task.id
	LEFT JOIN direct_dependents ON direct_dependents.prerequisite_task_id=task.id
		WHERE task.id IN (` + strings.Join(placeholders, ",") + `)
	  AND task.deleted_at IS NULL`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	byID := make(map[string]DependencySummary, len(ids))
	for rows.Next() {
		var id string
		var summary DependencySummary
		if err := rows.Scan(&id, &summary.PrerequisiteCount, &summary.UnmetPrerequisiteCount, &summary.DependentCount); err != nil {
			rows.Close()
			return err
		}
		summary.Blocked = summary.UnmetPrerequisiteCount > 0
		byID[id] = summary
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for i := range tasks {
		if summary, ok := byID[tasks[i].ID]; ok {
			tasks[i].DependencySummary = summary
		}
	}
	return nil
}

func (s *Store) populateDependencySummary(ctx context.Context, task *Task) error {
	if task == nil {
		return nil
	}
	tasks := []Task{*task}
	if err := s.populateTaskDependencySummaries(ctx, tasks); err != nil {
		return err
	}
	*task = tasks[0]
	return nil
}
