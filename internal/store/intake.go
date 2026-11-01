package store

import (
	"context"
	"strings"
	"time"
)

// IntakeFilter selects the union of existing ticket membership and bug kind.
// ProjectIDs nil means unrestricted; non-nil empty means no readable projects.
type IntakeFilter struct {
	ProjectIDs []string
	Scope      string
	Kind       string
	Severity   string
	Query      string
	Limit      int
	Cursor     string
	Reader     string
}

type IntakeCounts struct {
	All         int `json:"all"`
	NeedsTriage int `json:"needs_triage"`
}

// Membership predicates use EXISTS so an overlapping ticket/bug stays one task.
// Triage does not infer lifecycle state from claims, assignment or agent pulses.
const intakeNeedsTriage = `((EXISTS (SELECT 1 FROM tickets tk WHERE tk.task_id=t.id) AND c.semantic_state='backlog') OR (t.kind='bug' AND c.semantic_state<>'completed' AND EXISTS (SELECT 1 FROM bug_details bd WHERE bd.task_id=t.id AND bd.severity IS NULL AND bd.resolution IS NULL)))`

func intakeScope(filter IntakeFilter) (string, []any) {
	query := ` FROM tasks t JOIN columns c ON c.id=t.column_id JOIN projects p ON p.id=t.project_id WHERE t.deleted_at IS NULL AND p.archived_at IS NULL AND (t.kind='bug' OR EXISTS (SELECT 1 FROM tickets tk WHERE tk.task_id=t.id))`
	args := make([]any, 0)
	query, args = intakeProjectScope(query, args, filter.ProjectIDs)
	if filter.Kind != "" {
		query += ` AND t.kind=?`
		args = append(args, filter.Kind)
	}
	if filter.Severity != "" {
		query += ` AND t.kind='bug' AND EXISTS (SELECT 1 FROM bug_details bd WHERE bd.task_id=t.id AND `
		if filter.Severity == "none" || filter.Severity == "untriaged" {
			query += `bd.severity IS NULL)`
		} else {
			query += `bd.severity=?)`
			args = append(args, filter.Severity)
		}
	}
	if filter.Query != "" {
		query += ` AND (lower(t.title) LIKE ? OR lower(t.description) LIKE ? OR upper(p.key || '-' || t.number)=? OR EXISTS (SELECT 1 FROM task_number_aliases a JOIN projects ap ON ap.id=a.project_id WHERE a.task_id=t.id AND upper(ap.key || '-' || a.number)=?) OR EXISTS (SELECT 1 FROM bug_details bd WHERE bd.task_id=t.id AND (lower(bd.actual_behavior) LIKE ? OR lower(bd.expected_behavior) LIKE ? OR lower(bd.reproduction_steps) LIKE ? OR lower(bd.environment) LIKE ? OR lower(bd.affected_version) LIKE ?)))`
		like := "%" + strings.ToLower(filter.Query) + "%"
		args = append(args, like, like, strings.ToUpper(filter.Query), strings.ToUpper(filter.Query), like, like, like, like, like)
	}
	return query, args
}

// listIntake returns existing enriched task records without creating the queue
// or changing any membership. Ordering remains stable when priorities change.
func (s *Store) listIntake(ctx context.Context, filter IntakeFilter, cursor intakeCursor, readAt time.Time) ([]Task, bool, error) {
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	query, args := intakeScope(filter)
	switch filter.Scope {
	case "", "needs_triage":
		query += ` AND ` + intakeNeedsTriage
	case "all":
	default:
		return nil, false, invalid("unknown intake scope", nil)
	}
	if cursor.ID != "" {
		query += ` AND (t.created_at>? OR (t.created_at=? AND t.id>?))`
		args = append(args, cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	query = `SELECT ` + taskColumns + query + ` ORDER BY t.created_at,t.id LIMIT ?`
	args = append(args, filter.Limit+1)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	result := make([]Task, 0, filter.Limit)
	for rows.Next() {
		task, err := taskFromRow(rows)
		if err != nil {
			rows.Close()
			return nil, false, err
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, err
	}
	rows.Close()
	more := len(result) > filter.Limit
	if more {
		result = result[:filter.Limit]
	}
	for i := range result {
		if err := s.enrichTaskAt(ctx, &result[i], readAt); err != nil {
			return nil, false, err
		}
	}
	if err := s.populateTaskDependencySummaries(ctx, result); err != nil {
		return nil, false, err
	}
	if err := s.populateTaskReleaseReferences(ctx, result); err != nil {
		return nil, false, err
	}
	return result, more, nil
}

// CountIntake applies the same authorization and field predicates as the page,
// ignoring scope and pagination so both tabs have authoritative filtered counts.
func (s *Store) CountIntake(ctx context.Context, filter IntakeFilter) (IntakeCounts, error) {
	query, args := intakeScope(filter)
	var counts IntakeCounts
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1),COALESCE(SUM(CASE WHEN `+intakeNeedsTriage+` THEN 1 ELSE 0 END),0)`+query, args...).Scan(&counts.All, &counts.NeedsTriage)
	return counts, err
}
