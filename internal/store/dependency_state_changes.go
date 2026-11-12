package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type dependencyStateChange struct {
	PrerequisiteID  string
	ProjectID       string
	PrerequisiteKey string
	Satisfied       bool
}

func dependencyTaskStateChange(ctx context.Context, q dependencySQL, before dependencyTask) (dependencyStateChange, bool, error) {
	after, err := resolveDependencyTask(ctx, q, before.ID)
	if err != nil {
		return dependencyStateChange{}, false, err
	}
	beforeSatisfied := dependencySatisfied(before)
	afterSatisfied := dependencySatisfied(after)
	if beforeSatisfied == afterSatisfied {
		return dependencyStateChange{}, false, nil
	}
	return dependencyStateChange{
		PrerequisiteID:  after.ID,
		ProjectID:       after.ProjectID,
		PrerequisiteKey: after.Key,
		Satisfied:       afterSatisfied,
	}, true, nil
}

func dependencyColumnStateChanges(ctx context.Context, q dependencySQL, column Column, nextState, completedAt string) ([]dependencyStateChange, error) {
	if column.SemanticState == nextState || (column.SemanticState != "completed" && nextState != "completed") {
		return nil, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT t.id, t.project_id, p.key, t.number, t.title, t.completed_at
		FROM tasks t
		JOIN projects p ON p.id=t.project_id
		WHERE t.column_id=? AND t.deleted_at IS NULL
		ORDER BY t.number, t.id`, column.ID)
	if err != nil {
		return nil, err
	}
	changes := make([]dependencyStateChange, 0)
	for rows.Next() {
		var before dependencyTask
		if err := rows.Scan(&before.ID, &before.ProjectID, &before.ProjectKey, &before.Number, &before.Title, &before.CompletedAt); err != nil {
			rows.Close()
			return nil, err
		}
		before.Key = fmt.Sprintf("%s-%d", before.ProjectKey, before.Number)
		before.SemanticState = column.SemanticState
		after := before
		after.SemanticState = nextState
		if nextState == "completed" {
			after.CompletedAt = sql.NullString{String: completedAt, Valid: true}
		} else if column.SemanticState == "completed" {
			after.CompletedAt = sql.NullString{}
		}
		if dependencySatisfied(before) != dependencySatisfied(after) {
			changes = append(changes, dependencyStateChange{
				PrerequisiteID:  before.ID,
				ProjectID:       before.ProjectID,
				PrerequisiteKey: before.Key,
				Satisfied:       dependencySatisfied(after),
			})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return changes, nil
}

// emitDependencyStateChanges emits one event for every direct live dependent
// of each changed prerequisite. It reads the reverse edges in one bounded
// query and inserts the events only after closing the result set, keeping the
// helper safe for both *sql.Tx and *sql.Conn transactions.
func emitDependencyStateChanges(ctx context.Context, q dependencySQL, actorID string, changes []dependencyStateChange) error {
	if len(changes) == 0 {
		return nil
	}
	byPrerequisite := make(map[string]dependencyStateChange, len(changes))
	ids := make([]string, 0, len(changes))
	for _, change := range changes {
		if change.PrerequisiteID == "" {
			continue
		}
		if _, exists := byPrerequisite[change.PrerequisiteID]; exists {
			continue
		}
		byPrerequisite[change.PrerequisiteID] = change
		ids = append(ids, change.PrerequisiteID)
	}
	if len(ids) == 0 {
		return nil
	}
	type eventTarget struct {
		PrerequisiteID string
		DependentID    string
		DependentKey   string
	}
	targets := make([]eventTarget, 0)
	const batchSize = 500
	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		batch := ids[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for index, id := range batch {
			placeholders[index] = "?"
			args[index] = id
		}
		query := `SELECT td.prerequisite_task_id, dependent.id, dependent.number, dependent_project.key
			FROM task_dependencies td
			JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id AND prerequisite.deleted_at IS NULL
			JOIN tasks dependent ON dependent.id=td.task_id AND dependent.deleted_at IS NULL
			JOIN projects dependent_project ON dependent_project.id=dependent.project_id
			WHERE td.prerequisite_task_id IN (` + strings.Join(placeholders, ",") + `)
			  AND dependent.project_id=prerequisite.project_id
			ORDER BY td.prerequisite_task_id, dependent.number, dependent.id`
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var prerequisiteID, dependentID, projectKey string
			var number int
			if err := rows.Scan(&prerequisiteID, &dependentID, &number, &projectKey); err != nil {
				rows.Close()
				return err
			}
			targets = append(targets, eventTarget{
				PrerequisiteID: prerequisiteID,
				DependentID:    dependentID,
				DependentKey:   fmt.Sprintf("%s-%d", projectKey, number),
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	for _, target := range targets {
		change, ok := byPrerequisite[target.PrerequisiteID]
		if !ok {
			continue
		}
		payload := map[string]any{
			"dependent_id":     target.DependentID,
			"dependent_key":    target.DependentKey,
			"prerequisite_id":  change.PrerequisiteID,
			"prerequisite_key": change.PrerequisiteKey,
			"satisfied":        change.Satisfied,
		}
		eventID, createdAt := newID(), now()
		result, err := q.ExecContext(ctx, `INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?)`, eventID, "task.dependency_state_changed", actorID, change.ProjectID, target.DependentID, eventPayload(payload), createdAt)
		if err != nil {
			return err
		}
		eventCursor, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if err := notifyForEventTx(ctx, q, eventID, eventCursor, "task.dependency_state_changed", actorID, change.ProjectID, target.DependentID, eventPayload(payload), createdAt); err != nil && !isOptionalNotificationSchemaError(err) {
			return err
		}
	}
	return nil
}
