package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// getBugDetails loads the 1:1 detail row. A missing row is returned as nil so
// a partially upgraded/manual fixture remains readable; normal create and
// update paths always enforce the row's presence for kind=bug.
func (s *Store) getBugDetails(ctx context.Context, taskID string) (*Bug, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT reporter_id, severity, actual_behavior, expected_behavior, reproduction_steps, environment, affected_version, resolution, resolved_by, resolved_at, duplicate_of FROM bug_details WHERE task_id=?`, taskID)
	bug, err := bugFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &bug, nil
}

func bugFromRow(scanner interface{ Scan(...any) error }) (Bug, error) {
	var bug Bug
	var severity, resolution, resolvedBy, resolvedAt, duplicateOf sql.NullString
	if err := scanner.Scan(&bug.ReporterID, &severity, &bug.ActualBehavior, &bug.ExpectedBehavior, &bug.ReproductionSteps, &bug.Environment, &bug.AffectedVersion, &resolution, &resolvedBy, &resolvedAt, &duplicateOf); err != nil {
		return Bug{}, err
	}
	bug.Severity = nullableString(severity)
	bug.Resolution = nullableString(resolution)
	bug.ResolvedBy = nullableString(resolvedBy)
	bug.ResolvedAt = nullableString(resolvedAt)
	bug.DuplicateOf = nullableString(duplicateOf)
	return bug, nil
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func insertBugDetailsTx(ctx context.Context, tx *sql.Tx, taskID, reporterID string, input BugInput) error {
	if err := validateBugCreateInput(input); err != nil {
		return err
	}
	if strings.TrimSpace(reporterID) == "" {
		return invalid("a reporter actor is required for bugs", nil)
	}
	var severity any
	if input.Severity != nil {
		severity = *input.Severity
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO bug_details(task_id, reporter_id, severity, actual_behavior, expected_behavior, reproduction_steps, environment, affected_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, taskID, reporterID, severity, strings.TrimSpace(*input.ActualBehavior), stringOrEmpty(input.ExpectedBehavior), stringOrEmpty(input.ReproductionSteps), stringOrEmpty(input.Environment), stringOrEmpty(input.AffectedVersion))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "foreign key") {
			return invalid("reporter actor not found", nil)
		}
		return err
	}
	return nil
}

func updateBugDetailsTx(ctx context.Context, tx *sql.Tx, taskID string, input BugInput) error {
	row := tx.QueryRowContext(ctx, `SELECT reporter_id, severity, actual_behavior, expected_behavior, reproduction_steps, environment, affected_version, resolution, resolved_by, resolved_at, duplicate_of FROM bug_details WHERE task_id=?`, taskID)
	current, err := bugFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return invalid("bug details not found", nil)
	}
	if err != nil {
		return err
	}
	merged, err := mergeBugInput(current, input)
	if err != nil {
		return err
	}
	var severity any
	if merged.Severity != nil {
		severity = *merged.Severity
	}
	_, err = tx.ExecContext(ctx, `UPDATE bug_details SET severity=?, actual_behavior=?, expected_behavior=?, reproduction_steps=?, environment=?, affected_version=? WHERE task_id=?`, severity, merged.ActualBehavior, merged.ExpectedBehavior, merged.ReproductionSteps, merged.Environment, merged.AffectedVersion, taskID)
	return err
}

func mergeBugInput(current Bug, input BugInput) (Bug, error) {
	merged := current
	if input.SeveritySet || input.Severity != nil {
		merged.Severity = input.Severity
	}
	if input.ActualBehaviorSet || input.ActualBehavior != nil {
		if input.ActualBehavior == nil || strings.TrimSpace(*input.ActualBehavior) == "" {
			return Bug{}, invalid("actual_behavior must not be empty", nil)
		}
		merged.ActualBehavior = strings.TrimSpace(*input.ActualBehavior)
	}
	if input.ExpectedBehaviorSet || input.ExpectedBehavior != nil {
		merged.ExpectedBehavior = stringOrEmpty(input.ExpectedBehavior)
	}
	if input.ReproductionStepsSet || input.ReproductionSteps != nil {
		merged.ReproductionSteps = stringOrEmpty(input.ReproductionSteps)
	}
	if input.EnvironmentSet || input.Environment != nil {
		merged.Environment = stringOrEmpty(input.Environment)
	}
	if input.AffectedVersionSet || input.AffectedVersion != nil {
		merged.AffectedVersion = stringOrEmpty(input.AffectedVersion)
	}
	return merged, nil
}

// ListIssues returns all live bugs visible to the caller. ListIssuesWithExtra
// follows the same sentinel-row contract as ListTasksWithExtra and supports a
// ProjectIDs allow-list for global discovery endpoints.
func (s *Store) ListIssues(ctx context.Context, filter TaskFilter) ([]Task, bool, error) {
	return s.listIssues(ctx, filter, false)
}

func (s *Store) ListIssuesWithExtra(ctx context.Context, filter TaskFilter) ([]Task, bool, error) {
	return s.listIssues(ctx, filter, true)
}

// ListBugsWithExtra is a compatibility alias for clients that use the domain
// term rather than the API's global "issues" collection name.
func (s *Store) ListBugsWithExtra(ctx context.Context, filter TaskFilter) ([]Task, bool, error) {
	return s.ListIssuesWithExtra(ctx, filter)
}

func (s *Store) listIssues(ctx context.Context, filter TaskFilter, allowExtra bool) ([]Task, bool, error) {
	readAt := time.Now().UTC()
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	maxLimit := 200
	if allowExtra {
		maxLimit++
	}
	if filter.Limit > maxLimit {
		filter.Limit = maxLimit
	}
	query := `SELECT ` + taskColumns + ` FROM tasks t JOIN columns c ON c.id=t.column_id WHERE t.deleted_at IS NULL AND t.kind='bug'`
	args := make([]any, 0, 12)
	if filter.ProjectIDs != nil && len(filter.ProjectIDs) == 0 {
		query += ` AND 1=0`
	}
	if len(filter.ProjectIDs) > 0 {
		placeholders := make([]string, len(filter.ProjectIDs))
		for i, projectID := range filter.ProjectIDs {
			placeholders[i] = "?"
			args = append(args, projectID)
		}
		query += ` AND t.project_id IN (` + strings.Join(placeholders, ",") + `)`
	}
	query, args = appendIssueFilters(query, args, filter, readAt)
	query += ` ORDER BY t.project_id, c.position, t.position, t.number, t.id LIMIT ? OFFSET ?`
	args = append(args, filter.Limit+1, filter.Cursor)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]Task, 0, filter.Limit)
	for rows.Next() {
		task, err := taskFromRow(rows)
		if err != nil {
			return nil, false, err
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(result) > filter.Limit
	if hasMore {
		result = result[:filter.Limit]
	}
	for i := range result {
		if err := s.enrichTaskAt(ctx, &result[i], readAt); err != nil {
			return nil, false, err
		}
	}
	if err := s.populateTaskReleaseReferences(ctx, result); err != nil {
		return nil, false, err
	}
	return result, hasMore, nil
}

func appendIssueFilters(query string, args []any, filter TaskFilter, readAt time.Time) (string, []any) {
	if filter.State != "" {
		query += ` AND c.semantic_state=?`
		args = append(args, filter.State)
	}
	if filter.Column != "" {
		query += ` AND (t.column_id=? OR lower(c.name)=lower(?))`
		args = append(args, filter.Column, filter.Column)
	}
	if filter.Priority != "" {
		query += ` AND t.priority=?`
		args = append(args, filter.Priority)
	}
	if filter.Assignee != "" {
		if strings.EqualFold(filter.Assignee, "none") {
			query += ` AND t.assignee_id IS NULL`
		} else {
			query += ` AND t.assignee_id=?`
			args = append(args, filter.Assignee)
		}
	}
	if filter.Kind != "" {
		query += ` AND lower(t.kind)=lower(?)`
		args = append(args, filter.Kind)
	}
	if filter.Severity != "" {
		if strings.EqualFold(filter.Severity, "none") || strings.EqualFold(filter.Severity, "untriaged") {
			query += ` AND EXISTS (SELECT 1 FROM bug_details bd WHERE bd.task_id=t.id AND bd.severity IS NULL)`
		} else {
			query += ` AND EXISTS (SELECT 1 FROM bug_details bd WHERE bd.task_id=t.id AND bd.severity=?)`
			args = append(args, filter.Severity)
		}
	}
	if filter.Reporter != "" {
		query += ` AND EXISTS (SELECT 1 FROM bug_details bd WHERE bd.task_id=t.id AND bd.reporter_id=?)`
		args = append(args, filter.Reporter)
	}
	if filter.Resolution != "" {
		if strings.EqualFold(filter.Resolution, "none") || strings.EqualFold(filter.Resolution, "unresolved") {
			query += ` AND EXISTS (SELECT 1 FROM bug_details bd WHERE bd.task_id=t.id AND bd.resolution IS NULL)`
		} else {
			query += ` AND EXISTS (SELECT 1 FROM bug_details bd WHERE bd.task_id=t.id AND bd.resolution=?)`
			args = append(args, filter.Resolution)
		}
	}
	releaseFilter := filter.ReleaseID
	if releaseFilter == "" {
		releaseFilter = filter.Release
	}
	if releaseFilter != "" {
		if strings.EqualFold(strings.TrimSpace(releaseFilter), "unassigned") || strings.EqualFold(strings.TrimSpace(releaseFilter), "none") {
			query += ` AND t.release_id IS NULL`
		} else {
			query += ` AND t.release_id=?`
			args = append(args, strings.TrimSpace(releaseFilter))
		}
	}
	// A completed bug can retain its last agent snapshot for history, but it
	// no longer participates in liveness filtering until reopened.
	if filter.AgentState != "" || filter.ActionNeeded {
		query += ` AND t.completed_at IS NULL`
	}
	staleCutoff := agentWorkStaleCutoff(readAt)
	if filter.AgentState != "" {
		switch filter.AgentState {
		case "missing":
			query += ` AND NOT EXISTS (SELECT 1 FROM task_agent_work aw WHERE aw.task_id=t.id)`
		case "stale":
			query += ` AND EXISTS (SELECT 1 FROM task_agent_work aw WHERE aw.task_id=t.id AND julianday(aw.updated_at) <= julianday(?))`
			args = append(args, staleCutoff)
		default:
			query += ` AND EXISTS (SELECT 1 FROM task_agent_work aw WHERE aw.task_id=t.id AND aw.state=?)`
			args = append(args, filter.AgentState)
		}
	}
	if filter.ActionNeeded {
		query += ` AND EXISTS (SELECT 1 FROM task_agent_work aw WHERE aw.task_id=t.id AND (aw.state IN ('waiting', 'handoff') OR julianday(aw.updated_at) <= julianday(?)))`
		args = append(args, staleCutoff)
	}
	if filter.Label != "" {
		query += ` AND EXISTS (SELECT 1 FROM task_labels tl JOIN labels l ON l.id=tl.label_id WHERE tl.task_id=t.id AND (l.id=? OR lower(l.name)=lower(?)))`
		args = append(args, filter.Label, filter.Label)
	}
	if filter.Query != "" {
		query += ` AND (lower(t.title) LIKE ? OR lower(t.description) LIKE ? OR EXISTS (SELECT 1 FROM bug_details bd WHERE bd.task_id=t.id AND (lower(bd.actual_behavior) LIKE ? OR lower(bd.expected_behavior) LIKE ? OR lower(bd.reproduction_steps) LIKE ? OR lower(bd.environment) LIKE ? OR lower(bd.affected_version) LIKE ?)))`
		value := "%" + strings.ToLower(filter.Query) + "%"
		args = append(args, value, value, value, value, value, value, value)
	}
	if filter.UpdatedAfter != nil {
		query += ` AND t.updated_at>?`
		args = append(args, filter.UpdatedAfter.UTC().Format(time.RFC3339Nano))
	}
	return query, args
}
