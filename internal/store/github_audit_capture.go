package store

import (
	"context"
	"database/sql"
	"strings"
)

func captureGithubAuditTasks(ctx context.Context, tx *sql.Tx, projectID string, input GithubAuditStartInput) (GithubAuditCapture, error) {
	capture := GithubAuditCapture{Request: input, Tasks: []GithubAuditTask{}, Omissions: []string{}}
	query := `SELECT t.id,p.key||'-'||t.number,t.title,t.description,t.version,t.column_id,c.semantic_state,c.name,t.kind,p.checklist_completion_policy FROM tasks t JOIN projects p ON p.id=t.project_id JOIN columns c ON c.id=t.column_id WHERE t.project_id=? AND t.deleted_at IS NULL`
	args := []any{projectID}
	if len(input.TaskIDs) > 0 {
		parts := make([]string, len(input.TaskIDs))
		seen := map[string]bool{}
		for i, id := range input.TaskIDs {
			if seen[id] {
				return capture, invalid("duplicate task ID", nil)
			}
			seen[id] = true
			parts[i] = "?"
			args = append(args, id)
		}
		query += ` AND t.id IN (` + strings.Join(parts, ",") + `)`
	} else {
		states := input.States
		if len(states) == 0 {
			states = []string{"backlog", "ready", "active", "blocked", "completed"}
		}
		parts := make([]string, len(states))
		for i, state := range states {
			if !validState(state) {
				return capture, invalid("invalid audit task state", nil)
			}
			parts[i] = "?"
			args = append(args, state)
		}
		query += ` AND c.semantic_state IN (` + strings.Join(parts, ",") + `)`
	}
	query += ` ORDER BY t.id LIMIT 1001`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return capture, err
	}
	for rows.Next() {
		var task GithubAuditTask
		if err = rows.Scan(&task.ID, &task.Key, &task.Title, &task.Description, &task.Version, &task.ColumnID, &task.SemanticState, &task.ColumnName, &task.Kind, &task.ChecklistPolicy); err != nil {
			rows.Close()
			return capture, err
		}
		task.Criteria = []string{}
		capture.Tasks = append(capture.Tasks, task)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return capture, err
	}
	if len(capture.Tasks) > 1000 {
		return capture, invalid("audit scope exceeds hard batch limit of 1000 tasks; select fewer tasks or states", map[string]any{"limit": 1000, "at_least": 1001})
	}
	if len(input.TaskIDs) > 0 && len(capture.Tasks) != len(input.TaskIDs) {
		return capture, invalid("a requested task is outside this project or unavailable", nil)
	}
	for i := range capture.Tasks {
		capture.Tasks[i].Conditions = githubAuditDescriptionConditions(capture.Tasks[i])
		rows, err = tx.QueryContext(ctx, `SELECT id,text,completed FROM task_checklist_items WHERE task_id=? ORDER BY position,id`, capture.Tasks[i].ID)
		if err != nil {
			return capture, err
		}
		for rows.Next() {
			var id, criterion string
			var completed bool
			if err = rows.Scan(&id, &criterion, &completed); err != nil {
				rows.Close()
				return capture, err
			}
			capture.Tasks[i].Criteria = append(capture.Tasks[i].Criteria, criterion)
			condition := githubAuditCondition(capture.Tasks[i], "checklist", id, criterion, 0, 0)
			condition.Completed = &completed
			capture.Tasks[i].Conditions = append(capture.Tasks[i].Conditions, condition)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return capture, err
		}
	}
	if err = captureGithubAuditContext(ctx, tx, projectID, &capture); err != nil {
		return capture, err
	}
	return capture, nil
}
