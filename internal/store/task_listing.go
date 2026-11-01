package store

import (
	"context"
	"strings"
	"time"
)

func (s *Store) ListTasks(ctx context.Context, projectID string, filter TaskFilter) ([]Task, bool, error) {
	return s.listTasks(ctx, projectID, filter, false)
}

// ListTasksWithExtra lets a cursor endpoint fetch one sentinel row while the
// externally visible page size remains capped at 200.
func (s *Store) ListTasksWithExtra(ctx context.Context, projectID string, filter TaskFilter) ([]Task, bool, error) {
	return s.listTasks(ctx, projectID, filter, true)
}

// ListTasksCursor is the large-board task collection path. It always fetches
// one sentinel row and returns a strict keyset cursor for the last visible
// task. Retained callers can continue using ListTasks/ListTasksWithExtra with
// the integer offset in TaskFilter.Cursor.
func (s *Store) ListTasksCursor(ctx context.Context, projectID string, filter TaskFilter) ([]Task, bool, string, error) {
	return s.listTasksCursor(ctx, projectID, filter, true)
}

// taskCollectionSnapshot is the read-side boundary for keyset pagination.
// EventCursor is a durable, project-scoped revision: every task, column,
// label, comment, dependency, hierarchy, and agent-work mutation that can
// affect an enriched task collection emits a project event. Heartbeats are a
// deliberate exception because they only refresh task_agent_work.updated_at
// without changing the task version or emitting activity; the durable
// task-collection revision catches those changes so action-needed/stale
// membership cannot drift across pages. The revision is intentionally generic
// so future eventless collection mutations can share the same escape hatch.
type taskCollectionSnapshot struct {
	EventCursor            int64
	TaskCollectionRevision int64
}

func (s *Store) taskCollectionSnapshot(ctx context.Context, projectID string) (taskCollectionSnapshot, error) {
	var snapshot taskCollectionSnapshot
	// Keep both boundaries in one statement so they come from one SQLite read
	// snapshot. The project event revision is indexed by project and the task
	// collection revision is a single project row, making this O(1) regardless of board
	// size. COALESCE preserves the retained ListTasks behavior for an unknown
	// project, which simply has no matching task rows.
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT MAX(cursor) FROM events WHERE project_id=?), 0), COALESCE((SELECT task_collection_revision FROM projects WHERE id=?), 0)`, projectID, projectID).Scan(&snapshot.EventCursor, &snapshot.TaskCollectionRevision); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func taskCollectionChanged(expected, current taskCollectionSnapshot) error {
	details := map[string]any{
		"expected_revision":                 expected.EventCursor,
		"current_revision":                  current.EventCursor,
		"expected_task_collection_revision": expected.TaskCollectionRevision,
		"current_task_collection_revision":  current.TaskCollectionRevision,
		"restart":                           true,
	}
	return &Error{Kind: ErrTaskCollectionChanged, Message: "task collection changed; restart pagination from the first page", Details: details}
}

func (s *Store) listTasks(ctx context.Context, projectID string, filter TaskFilter, allowExtra bool) ([]Task, bool, error) {
	tasks, more, _, err := s.listTasksCursor(ctx, projectID, filter, allowExtra)
	return tasks, more, err
}

func (s *Store) listTasksCursor(ctx context.Context, projectID string, filter TaskFilter, allowExtra bool) ([]Task, bool, string, error) {
	sortName, err := NormalizeTaskSort(filter.Sort)
	if err != nil {
		return nil, false, "", invalid(err.Error(), nil)
	}
	filter.Sort = sortName
	var cursor TaskCursor
	useKeyset := strings.TrimSpace(filter.CursorToken) != ""
	readAt := time.Now().UTC()
	var expectedSnapshot taskCollectionSnapshot
	if useKeyset {
		cursor, err = DecodeTaskCursor(filter.CursorToken)
		if err != nil {
			return nil, false, "", invalid(err.Error(), nil)
		}
		if cursor.Sort != filter.Sort || cursor.Descending != filter.Descending {
			return nil, false, "", invalid("cursor does not match the requested sort", nil)
		}
		if cursor.ProjectID != projectID {
			return nil, false, "", invalid("cursor does not match the requested project", nil)
		}
		readAt, err = time.Parse(time.RFC3339Nano, cursor.ReadAt)
		if err != nil {
			return nil, false, "", invalid("cursor is invalid", nil)
		}
		readAt = readAt.UTC()
	}
	expectedSnapshot, err = s.taskCollectionSnapshot(ctx, projectID)
	if err != nil {
		return nil, false, "", err
	}
	if useKeyset && (expectedSnapshot.EventCursor != cursor.CollectionRevision || expectedSnapshot.TaskCollectionRevision != cursor.TaskCollectionRevision) {
		return nil, false, "", taskCollectionChanged(taskCollectionSnapshot{EventCursor: cursor.CollectionRevision, TaskCollectionRevision: cursor.TaskCollectionRevision}, expectedSnapshot)
	}
	staleCutoff := agentWorkStaleCutoff(readAt)
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
	query := `SELECT ` + taskColumns + ` FROM tasks t JOIN columns c ON c.id=t.column_id JOIN projects p ON p.id=t.project_id WHERE t.project_id = ? AND t.deleted_at IS NULL`
	args := []any{projectID}
	if filter.State != "" {
		query += ` AND c.semantic_state = ?`
		args = append(args, filter.State)
	}
	if filter.Column != "" {
		query += ` AND (t.column_id = ? OR lower(c.name) = lower(?))`
		args = append(args, filter.Column, filter.Column)
	}
	if filter.Priority != "" {
		query += ` AND t.priority = ?`
		args = append(args, filter.Priority)
	}
	if filter.Assignee != "" {
		if strings.EqualFold(filter.Assignee, "none") {
			query += ` AND t.assignee_id IS NULL`
		} else {
			query += ` AND t.assignee_id = ?`
			args = append(args, filter.Assignee)
		}
	}
	if filter.Kind != "" {
		query += ` AND lower(t.kind) = lower(?)`
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
			query += ` AND t.release_id = ?`
			args = append(args, strings.TrimSpace(releaseFilter))
		}
	}
	if filter.Dependency != "" {
		switch strings.ToLower(strings.TrimSpace(filter.Dependency)) {
		case "blocked":
			query += ` AND EXISTS (
				SELECT 1
				FROM task_dependencies td
				JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
				JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
				WHERE td.task_id=t.id
				  AND prerequisite.project_id=t.project_id
				  AND prerequisite.deleted_at IS NULL
				  AND (prerequisite.completed_at IS NULL OR prerequisite_column.semantic_state <> 'completed'))`
		case "ready":
			query += ` AND EXISTS (
				SELECT 1 FROM task_dependencies td
				JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
				WHERE td.task_id=t.id
				  AND prerequisite.project_id=t.project_id
				  AND prerequisite.deleted_at IS NULL)
			AND NOT EXISTS (
				SELECT 1
				FROM task_dependencies td
				JOIN tasks prerequisite ON prerequisite.id=td.prerequisite_task_id
				JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
				WHERE td.task_id=t.id
				  AND prerequisite.project_id=t.project_id
				  AND prerequisite.deleted_at IS NULL
				  AND (prerequisite.completed_at IS NULL OR prerequisite_column.semantic_state <> 'completed'))`
		default:
			return nil, false, "", invalid("dependency must be blocked or ready", map[string]any{"dependency": filter.Dependency})
		}
	}
	// Completion suppresses the liveness classification of a retained
	// snapshot. Keep completed tasks visible in ordinary board listings, but
	// never let them satisfy an agent-state/action-needed filter.
	if filter.AgentState != "" || filter.ActionNeeded {
		query += ` AND t.completed_at IS NULL`
	}
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
		query += ` AND (lower(p.key || '-' || CAST(t.number AS TEXT)) LIKE ? OR lower(t.title) LIKE ? OR lower(t.description) LIKE ? OR EXISTS (SELECT 1 FROM bug_details bd WHERE bd.task_id=t.id AND (lower(bd.actual_behavior) LIKE ? OR lower(bd.expected_behavior) LIKE ? OR lower(bd.reproduction_steps) LIKE ? OR lower(bd.environment) LIKE ? OR lower(bd.affected_version) LIKE ?)))`
		q := "%" + strings.ToLower(filter.Query) + "%"
		args = append(args, q, q, q, q, q, q, q, q)
	}
	if filter.UpdatedAfter != nil {
		query += ` AND t.updated_at > ?`
		args = append(args, filter.UpdatedAfter.UTC().Format(time.RFC3339Nano))
	}
	if useKeyset {
		query, args, err = appendTaskCursorPredicate(query, args, cursor, filter.Descending)
		if err != nil {
			return nil, false, "", invalid(err.Error(), nil)
		}
	}
	order, err := taskOrderSQL(filter.Sort, filter.Descending)
	if err != nil {
		return nil, false, "", invalid(err.Error(), nil)
	}
	// Offset remains available only for retained callers that do not provide a
	// keyset token. New board requests use a strict tuple predicate and start
	// from the first matching row without paying an ever-growing OFFSET cost.
	query += ` ORDER BY ` + order + ` LIMIT ?`
	args = append(args, filter.Limit+1)
	if !useKeyset {
		query += ` OFFSET ?`
		args = append(args, filter.Cursor)
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, "", err
	}
	result := make([]Task, 0, filter.Limit)
	for rows.Next() {
		task, err := taskFromRow(rows)
		if err != nil {
			rows.Close()
			return nil, false, "", err
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, "", err
	}
	rows.Close()
	hasMore := len(result) > filter.Limit
	nextCursor := ""
	if hasMore {
		result = result[:filter.Limit]
	}
	for i := range result {
		if err := s.enrichTaskAt(ctx, &result[i], readAt); err != nil {
			return nil, false, "", err
		}
	}
	if err := s.populateTaskDependencySummaries(ctx, result); err != nil {
		return nil, false, "", err
	}
	if err := s.populateTaskReleaseReferences(ctx, result); err != nil {
		return nil, false, "", err
	}
	currentSnapshot, err := s.taskCollectionSnapshot(ctx, projectID)
	if err != nil {
		return nil, false, "", err
	}
	if currentSnapshot != expectedSnapshot {
		return nil, false, "", taskCollectionChanged(expectedSnapshot, currentSnapshot)
	}
	if hasMore && len(result) > 0 {
		nextCursor = EncodeTaskCursor(result[len(result)-1], filter.Sort, filter.Descending, currentSnapshot.EventCursor, readAt, currentSnapshot.TaskCollectionRevision)
	}
	return result, hasMore, nextCursor, nil
}
