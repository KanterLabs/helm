package store

import (
	"context"
	"database/sql"
	"strings"
)

func captureGithubAuditContext(ctx context.Context, tx *sql.Tx, projectID string, c *GithubAuditCapture) error {
	c.AssessmentVersion = GithubAssessmentVersion
	c.Columns = []Column{}
	rows, err := tx.QueryContext(ctx, `SELECT id,project_id,name,semantic_state,position,version,created_at,updated_at,ordering_version FROM columns WHERE project_id=? AND archived_at IS NULL ORDER BY position,id`, projectID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var col Column
		if err = rows.Scan(&col.ID, &col.ProjectID, &col.Name, &col.SemanticState, &col.Position, &col.Version, &col.CreatedAt, &col.UpdatedAt, &col.OrderingVersion); err != nil {
			rows.Close()
			return err
		}
		c.Columns = append(c.Columns, col)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i := range c.Tasks {
		task := &c.Tasks[i]
		task.Context.Dependencies = []GithubAuditDependency{}
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(claimed_by,''),COALESCE(claim_expires_at,'') FROM tasks WHERE id=?`, task.ID).Scan(&task.Context.ClaimedBy, &task.Context.ClaimExpiresAt); err != nil {
			return err
		}
		rows, err = tx.QueryContext(ctx, `SELECT p.id,p.version,c.semantic_state FROM task_dependencies d JOIN tasks p ON p.id=d.prerequisite_task_id JOIN columns c ON c.id=p.column_id WHERE d.task_id=? AND p.deleted_at IS NULL ORDER BY p.id`, task.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d GithubAuditDependency
			if err = rows.Scan(&d.TaskID, &d.Version, &d.SemanticState); err != nil {
				rows.Close()
				return err
			}
			task.Context.Dependencies = append(task.Context.Dependencies, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		// Normal block-with-reason writes comment.created immediately before
		// task.blocked inside one transaction. Bind that exact event/comment pair;
		// never infer a blocker from a nearby timestamp or an arbitrary old comment.
		if task.SemanticState == "blocked" {
			err = tx.QueryRowContext(ctx, `SELECT c.id,c.body FROM events b
    JOIN events n ON n.cursor=b.cursor-1 AND n.type='comment.created' AND n.task_id=b.task_id AND n.actor_id=b.actor_id
    JOIN comments c ON c.id=json_extract(n.payload,'$.comment_id') AND c.task_id=b.task_id AND c.deleted_at IS NULL
    WHERE b.task_id=? AND b.type='task.blocked' AND b.cursor=(SELECT MAX(cursor) FROM events WHERE task_id=? AND type IN ('task.blocked','task.completed','task.reopened','task.moved'))`, task.ID, task.ID).Scan(&task.Context.BlockerSourceID, &task.Context.Blocker)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		// Explicit declarations preserve affirmative context without treating arbitrary
		// prose, a checklist tick or missing code as proof of an unstarted task.
		for _, line := range strings.Split(task.Description, "\n") {
			label, value, ok := strings.Cut(strings.TrimSpace(line), ":")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			switch strings.ToLower(label) {
			case "work status":
				task.Context.Unstarted = strings.EqualFold(value, "unstarted")
			case "readiness":
				if value == "ready" || value == "not_ready" {
					task.Context.Readiness = value
				}
			case "blocker":
				task.Context.Blocker = value
				task.Context.BlockerSourceID = "description"
			}
		}
	}
	return nil
}
