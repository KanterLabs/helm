package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// ProjectIntelligenceMetrics is a bounded, point-in-time aggregate used for
// deterministic project ordering and optional Luna analysis. It deliberately
// contains no task text, comments, actor names, or progress narrative.
type ProjectIntelligenceMetrics struct {
	OpenTasks           int      `json:"open_tasks"`
	CompletedTasks      int      `json:"completed_tasks"`
	ActiveTasks         int      `json:"active_tasks"`
	BlockedTasks        int      `json:"blocked_tasks"`
	OverdueTasks        int      `json:"overdue_tasks"`
	DueSoonTasks        int      `json:"due_soon_tasks"`
	UrgentTasks         int      `json:"urgent_tasks"`
	HighPriorityTasks   int      `json:"high_priority_tasks"`
	ActionNeeded        int      `json:"action_needed"`
	StaleAgentWork      int      `json:"stale_agent_work"`
	DependencyBlocked   int      `json:"dependency_blocked"`
	DirectUnblockCount  int      `json:"direct_unblock_count"`
	SevereBugs          int      `json:"severe_bugs"`
	UnreadNotifications int      `json:"unread_notifications"`
	Created7d           int      `json:"created_7d"`
	Completed7d         int      `json:"completed_7d"`
	NetOpenChange7d     int      `json:"net_open_change_7d"`
	PlannedFocusCount   int      `json:"planned_focus_count"`
	FocusDueSoon        int      `json:"focus_due_soon"`
	OldestActiveDays    *float64 `json:"oldest_active_days,omitempty"`
	LastActivityAt      *string  `json:"last_activity_at,omitempty"`
}

// ProjectIntelligenceProject identifies one visible project and its aggregate
// snapshot. Project metadata is included only so clients can render a stable
// result without joining untrusted model output to arbitrary text.
type ProjectIntelligenceProject struct {
	ID       string                     `json:"project_id"`
	Key      string                     `json:"key"`
	Slug     string                     `json:"slug"`
	Name     string                     `json:"name"`
	Color    string                     `json:"color"`
	Favorite bool                       `json:"favorite"`
	Metrics  ProjectIntelligenceMetrics `json:"metrics"`
}

// ProjectIntelligenceSnapshot returns at most 200 live projects using one
// aggregate SQL statement. A nil project ceiling means all projects; a
// non-nil empty ceiling intentionally returns none.
func (s *Store) ProjectIntelligenceSnapshot(ctx context.Context, actorID string, projectIDs []string, asOf time.Time) ([]ProjectIntelligenceProject, error) {
	asOf = asOf.UTC()
	nowText := asOf.Format(time.RFC3339Nano)
	staleText := agentWorkStaleCutoff(asOf)
	sevenDaysAgo := asOf.Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano)

	projectPredicate := ""
	args := []any{nowText, nowText, nowText, nowText, staleText, staleText, sevenDaysAgo, sevenDaysAgo, nowText, nowText, actorID}
	if projectIDs != nil && len(projectIDs) == 0 {
		projectPredicate = " AND 1=0"
	} else if len(projectIDs) > 0 {
		placeholders := make([]string, len(projectIDs))
		for index, projectID := range projectIDs {
			placeholders[index] = "?"
			args = append(args, projectID)
		}
		projectPredicate = " AND p.id IN (" + strings.Join(placeholders, ",") + ")"
	}

	rows, err := s.DB.QueryContext(ctx, `
		WITH task_rollup AS (
			SELECT t.project_id,
				SUM(CASE WHEN t.completed_at IS NULL THEN 1 ELSE 0 END) AS open_tasks,
				SUM(CASE WHEN t.completed_at IS NOT NULL THEN 1 ELSE 0 END) AS completed_tasks,
				SUM(CASE WHEN t.completed_at IS NULL AND c.semantic_state='active' THEN 1 ELSE 0 END) AS active_tasks,
				SUM(CASE WHEN t.completed_at IS NULL AND c.semantic_state='blocked' THEN 1 ELSE 0 END) AS blocked_tasks,
				SUM(CASE WHEN t.completed_at IS NULL AND t.due_at IS NOT NULL AND julianday(t.due_at) < julianday(?) THEN 1 ELSE 0 END) AS overdue_tasks,
				SUM(CASE WHEN t.completed_at IS NULL AND t.due_at IS NOT NULL AND julianday(t.due_at) >= julianday(?) AND julianday(t.due_at) <= julianday(?, '+7 days') THEN 1 ELSE 0 END) AS due_soon_tasks,
				SUM(CASE WHEN t.completed_at IS NULL AND t.priority='urgent' THEN 1 ELSE 0 END) AS urgent_tasks,
				SUM(CASE WHEN t.completed_at IS NULL AND t.priority='high' THEN 1 ELSE 0 END) AS high_priority_tasks,
				CAST(MAX(CASE WHEN t.completed_at IS NULL AND c.semantic_state='active' THEN julianday(?) - julianday(t.created_at) END) AS INTEGER) AS oldest_active_days
			FROM tasks t JOIN columns c ON c.id=t.column_id
			WHERE t.deleted_at IS NULL
			GROUP BY t.project_id
		), agent_rollup AS (
			SELECT t.project_id,
				SUM(CASE WHEN aw.state IN ('waiting','handoff') OR julianday(aw.updated_at) <= julianday(?) THEN 1 ELSE 0 END) AS action_needed,
				SUM(CASE WHEN julianday(aw.updated_at) <= julianday(?) THEN 1 ELSE 0 END) AS stale_agent_work
			FROM task_agent_work aw JOIN tasks t ON t.id=aw.task_id
			WHERE t.deleted_at IS NULL AND t.completed_at IS NULL
			GROUP BY t.project_id
		), dependency_rollup AS (
			SELECT dependent.project_id,
				COUNT(DISTINCT dependent.id) AS dependency_blocked
			FROM task_dependencies d
			JOIN tasks dependent ON dependent.id=d.task_id
			JOIN tasks prerequisite ON prerequisite.id=d.prerequisite_task_id
			LEFT JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
			WHERE dependent.deleted_at IS NULL AND dependent.completed_at IS NULL
			  AND prerequisite.deleted_at IS NULL
			  AND (prerequisite.completed_at IS NULL OR prerequisite_column.semantic_state<>'completed')
			GROUP BY dependent.project_id
		), unblock_rollup AS (
			SELECT prerequisite.project_id,
				COUNT(DISTINCT dependent.id) AS direct_unblock_count
			FROM task_dependencies d
			JOIN tasks dependent ON dependent.id=d.task_id
			JOIN tasks prerequisite ON prerequisite.id=d.prerequisite_task_id
			LEFT JOIN columns prerequisite_column ON prerequisite_column.id=prerequisite.column_id
			WHERE dependent.deleted_at IS NULL AND dependent.completed_at IS NULL
			  AND prerequisite.deleted_at IS NULL
			  AND (prerequisite.completed_at IS NULL OR prerequisite_column.semantic_state<>'completed')
			GROUP BY prerequisite.project_id
		), bug_rollup AS (
			SELECT t.project_id,
				SUM(CASE WHEN b.resolved_at IS NULL AND b.severity IN ('s1','s2') THEN 1 ELSE 0 END) AS severe_bugs
			FROM tasks t JOIN bug_details b ON b.task_id=t.id
			WHERE t.deleted_at IS NULL
			GROUP BY t.project_id
		), event_rollup AS (
			SELECT e.project_id,
				COUNT(DISTINCT CASE WHEN e.created_at>=? AND e.type IN ('task.created','bug.created') THEN e.task_id END) AS created_7d,
				COUNT(DISTINCT CASE WHEN e.created_at>=? AND e.type IN ('task.completed','bug.resolved') THEN e.task_id END) AS completed_7d,
				MAX(e.created_at) AS last_activity_at
			FROM events e
			WHERE e.project_id IS NOT NULL AND e.created_at<=?
			GROUP BY e.project_id
		), focus_rollup AS (
			SELECT r.project_id,
				COUNT(1) AS planned_focus_count,
				SUM(CASE WHEN r.target_date IS NOT NULL AND date(r.target_date)<=date(?, '+7 days') THEN 1 ELSE 0 END) AS focus_due_soon
			FROM releases r
			WHERE r.released_at IS NULL
			GROUP BY r.project_id
		), notification_rollup AS (
			SELECT n.project_id, COUNT(1) AS unread_notifications
			FROM notifications n
			WHERE n.recipient_id=? AND n.read_at IS NULL AND n.project_id IS NOT NULL
			GROUP BY n.project_id
		)
		SELECT p.id, p.key, p.slug, p.name, p.color, p.favorite,
			COALESCE(t.open_tasks,0), COALESCE(t.completed_tasks,0), COALESCE(t.active_tasks,0), COALESCE(t.blocked_tasks,0),
			COALESCE(t.overdue_tasks,0), COALESCE(t.due_soon_tasks,0), COALESCE(t.urgent_tasks,0), COALESCE(t.high_priority_tasks,0),
			COALESCE(a.action_needed,0), COALESCE(a.stale_agent_work,0), COALESCE(d.dependency_blocked,0), COALESCE(u.direct_unblock_count,0),
			COALESCE(b.severe_bugs,0), COALESCE(n.unread_notifications,0), COALESCE(e.created_7d,0), COALESCE(e.completed_7d,0),
			COALESCE(e.created_7d,0)-COALESCE(e.completed_7d,0), COALESCE(f.planned_focus_count,0), COALESCE(f.focus_due_soon,0),
			t.oldest_active_days, e.last_activity_at
		FROM projects p
		LEFT JOIN task_rollup t ON t.project_id=p.id
		LEFT JOIN agent_rollup a ON a.project_id=p.id
		LEFT JOIN dependency_rollup d ON d.project_id=p.id
		LEFT JOIN unblock_rollup u ON u.project_id=p.id
		LEFT JOIN bug_rollup b ON b.project_id=p.id
		LEFT JOIN event_rollup e ON e.project_id=p.id
		LEFT JOIN focus_rollup f ON f.project_id=p.id
		LEFT JOIN notification_rollup n ON n.project_id=p.id
		WHERE p.archived_at IS NULL`+projectPredicate+`
		ORDER BY p.favorite DESC, lower(p.name), p.id
		LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ProjectIntelligenceProject, 0)
	for rows.Next() {
		var project ProjectIntelligenceProject
		var favorite int
		var oldest sql.NullFloat64
		var activity sql.NullString
		metrics := &project.Metrics
		if err := rows.Scan(
			&project.ID, &project.Key, &project.Slug, &project.Name, &project.Color, &favorite,
			&metrics.OpenTasks, &metrics.CompletedTasks, &metrics.ActiveTasks, &metrics.BlockedTasks,
			&metrics.OverdueTasks, &metrics.DueSoonTasks, &metrics.UrgentTasks, &metrics.HighPriorityTasks,
			&metrics.ActionNeeded, &metrics.StaleAgentWork, &metrics.DependencyBlocked, &metrics.DirectUnblockCount,
			&metrics.SevereBugs, &metrics.UnreadNotifications, &metrics.Created7d, &metrics.Completed7d,
			&metrics.NetOpenChange7d, &metrics.PlannedFocusCount, &metrics.FocusDueSoon,
			&oldest, &activity,
		); err != nil {
			return nil, err
		}
		project.Favorite = favorite != 0
		if oldest.Valid {
			value := oldest.Float64
			metrics.OldestActiveDays = &value
		}
		if activity.Valid {
			value := activity.String
			metrics.LastActivityAt = &value
		}
		result = append(result, project)
	}
	return result, rows.Err()
}
