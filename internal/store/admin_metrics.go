package store

import (
	"context"
	"time"
)

// AdminMetricTotals is a point-in-time snapshot of the whole installation.
type AdminMetricTotals struct {
	Projects     int `json:"projects"`
	TasksOpen    int `json:"tasks_open"`
	TasksDone    int `json:"tasks_done"`
	BugsOpen     int `json:"bugs_open"`
	ClaimsActive int `json:"claims_active"`
	Humans       int `json:"humans"`
	Agents       int `json:"agents"`
	ActiveTokens int `json:"active_tokens"`
}

// AdminMetricActivity counts persisted events inside the requested window.
type AdminMetricActivity struct {
	TasksCreated    int `json:"tasks_created"`
	TasksCompleted  int `json:"tasks_completed"`
	BugsReported    int `json:"bugs_reported"`
	BugsResolved    int `json:"bugs_resolved"`
	Comments        int `json:"comments"`
	Claims          int `json:"claims"`
	ProgressUpdates int `json:"progress_updates"`
	AgentEvents     int `json:"agent_events"`
	HumanEvents     int `json:"human_events"`
}

// AdminMetricDay is one UTC calendar day of persisted activity.
type AdminMetricDay struct {
	Date           string `json:"date"`
	TasksCreated   int    `json:"tasks_created"`
	TasksCompleted int    `json:"tasks_completed"`
	Comments       int    `json:"comments"`
	AgentEvents    int    `json:"agent_events"`
	HumanEvents    int    `json:"human_events"`
}

// AdminMetricAgent summarizes one agent's persisted activity in the window.
type AdminMetricAgent struct {
	ActorID         string  `json:"actor_id"`
	Name            string  `json:"name"`
	Disabled        bool    `json:"disabled"`
	Events          int     `json:"events"`
	Claims          int     `json:"claims"`
	Completions     int     `json:"completions"`
	LastEventAt     *string `json:"last_event_at"`
	TokenLastUsedAt *string `json:"token_last_used_at"`
}

// AdminMetrics is the persisted part of the administrator metrics view.
// Everything is derived from existing tables so the feature needs no schema
// change; the process-local request counters are added by the HTTP layer.
type AdminMetrics struct {
	Totals   AdminMetricTotals   `json:"totals"`
	Activity AdminMetricActivity `json:"activity"`
	Daily    []AdminMetricDay    `json:"daily"`
	Agents   []AdminMetricAgent  `json:"agents"`
}

// AdminMetricsSince aggregates installation-wide metrics for events at or
// after since (UTC). Callers must restrict this to administrators: it is not
// project-scoped.
func (s *Store) AdminMetricsSince(ctx context.Context, since time.Time) (AdminMetrics, error) {
	since = since.UTC().Truncate(24 * time.Hour)
	sinceText := since.Format("2006-01-02")
	nowText := now()
	result := AdminMetrics{Daily: []AdminMetricDay{}, Agents: []AdminMetricAgent{}}

	t := &result.Totals
	if err := s.DB.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(1) FROM projects WHERE archived_at IS NULL),
		(SELECT COUNT(1) FROM tasks WHERE deleted_at IS NULL AND completed_at IS NULL),
		(SELECT COUNT(1) FROM tasks WHERE deleted_at IS NULL AND completed_at IS NOT NULL),
		(SELECT COUNT(1) FROM tasks t JOIN bug_details b ON b.task_id=t.id WHERE t.deleted_at IS NULL AND b.resolved_at IS NULL),
		(SELECT COUNT(1) FROM tasks WHERE deleted_at IS NULL AND claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) > julianday(?)),
		(SELECT COUNT(1) FROM actors WHERE kind='human' AND disabled_at IS NULL),
		(SELECT COUNT(1) FROM actors WHERE kind='agent' AND disabled_at IS NULL),
		(SELECT COUNT(1) FROM tokens k JOIN actors a ON a.id=k.actor_id WHERE a.disabled_at IS NULL AND (k.expires_at IS NULL OR julianday(k.expires_at) > julianday(?)))`,
		nowText, nowText,
	).Scan(&t.Projects, &t.TasksOpen, &t.TasksDone, &t.BugsOpen, &t.ClaimsActive, &t.Humans, &t.Agents, &t.ActiveTokens); err != nil {
		return result, err
	}

	a := &result.Activity
	if err := s.DB.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(e.type='task.created'), 0),
		COALESCE(SUM(e.type='task.completed'), 0),
		COALESCE(SUM(e.type='task.created' AND t.kind='bug'), 0),
		COALESCE(SUM(e.type='comment.created'), 0),
		COALESCE(SUM(e.type='task.claimed'), 0),
		COALESCE(SUM(e.type='task.progressed'), 0),
		COALESCE(SUM(ac.kind='agent'), 0),
		COALESCE(SUM(ac.kind='human'), 0)
		FROM events e
		LEFT JOIN tasks t ON t.id=e.task_id
		LEFT JOIN actors ac ON ac.id=e.actor_id
		WHERE e.created_at >= ?`, sinceText,
	).Scan(&a.TasksCreated, &a.TasksCompleted, &a.BugsReported, &a.Comments, &a.Claims, &a.ProgressUpdates, &a.AgentEvents, &a.HumanEvents); err != nil {
		return result, err
	}
	// Bug resolution is recorded on bug_details rather than as a single event
	// type, so count it from the lifecycle column.
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM bug_details b JOIN tasks t ON t.id=b.task_id
		WHERE t.deleted_at IS NULL AND b.resolved_at IS NOT NULL AND b.resolved_at >= ?`, sinceText,
	).Scan(&a.BugsResolved); err != nil {
		return result, err
	}

	byDate := map[string]*AdminMetricDay{}
	for day := since; !day.After(time.Now().UTC()); day = day.AddDate(0, 0, 1) {
		entry := AdminMetricDay{Date: day.Format("2006-01-02")}
		result.Daily = append(result.Daily, entry)
	}
	for index := range result.Daily {
		byDate[result.Daily[index].Date] = &result.Daily[index]
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT substr(e.created_at, 1, 10) AS day,
		COALESCE(SUM(e.type='task.created'), 0),
		COALESCE(SUM(e.type='task.completed'), 0),
		COALESCE(SUM(e.type='comment.created'), 0),
		COALESCE(SUM(ac.kind='agent'), 0),
		COALESCE(SUM(ac.kind='human'), 0)
		FROM events e LEFT JOIN actors ac ON ac.id=e.actor_id
		WHERE e.created_at >= ?
		GROUP BY day`, sinceText)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var day AdminMetricDay
		if err := rows.Scan(&day.Date, &day.TasksCreated, &day.TasksCompleted, &day.Comments, &day.AgentEvents, &day.HumanEvents); err != nil {
			rows.Close()
			return result, err
		}
		if entry := byDate[day.Date]; entry != nil {
			*entry = day
		}
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	agentRows, err := s.DB.QueryContext(ctx, `SELECT a.id, a.name, a.disabled_at IS NOT NULL,
		COALESCE(ev.events, 0), COALESCE(ev.claims, 0), COALESCE(ev.completions, 0), ev.last_event_at,
		(SELECT MAX(k.last_used_at) FROM tokens k WHERE k.actor_id=a.id)
		FROM actors a
		LEFT JOIN (
			SELECT actor_id, COUNT(1) AS events,
				SUM(type='task.claimed') AS claims,
				SUM(type='task.completed') AS completions,
				MAX(created_at) AS last_event_at
			FROM events WHERE created_at >= ? AND actor_id IS NOT NULL
			GROUP BY actor_id
		) ev ON ev.actor_id=a.id
		WHERE a.kind='agent'
		ORDER BY COALESCE(ev.events, 0) DESC, a.name
		LIMIT 50`, sinceText)
	if err != nil {
		return result, err
	}
	defer agentRows.Close()
	for agentRows.Next() {
		var agent AdminMetricAgent
		if err := agentRows.Scan(&agent.ActorID, &agent.Name, &agent.Disabled, &agent.Events, &agent.Claims, &agent.Completions, &agent.LastEventAt, &agent.TokenLastUsedAt); err != nil {
			return result, err
		}
		result.Agents = append(result.Agents, agent)
	}
	return result, agentRows.Err()
}
