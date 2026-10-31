package store

import (
	"context"
	"errors"
	"time"
)

func (s *Store) Roadmap(ctx context.Context, projectID string) (Roadmap, error) {
	return s.RoadmapScoped(ctx, projectID, nil)
}

// RoadmapScoped applies one live project ceiling to totals, due dates,
// upcoming tasks, project summaries and recent activity. Nil is unrestricted;
// a nonnil empty ceiling must yield no data in every embedded collection.
func (s *Store) RoadmapScoped(ctx context.Context, projectID string, projectIDs []string) (Roadmap, error) {
	if projectID != "" {
		if projectIDs != nil {
			allowed := false
			for _, id := range projectIDs {
				if id == projectID {
					allowed = true
					break
				}
			}
			if !allowed {
				return Roadmap{}, forbidden("project access is required")
			}
		}
		projectIDs = []string{projectID}
	}
	scopeSQL, scopeArgs := appendProjectCeiling("", nil, projectIDs)
	var roadmap Roadmap
	roadmap.StateCounts = map[string]int{"backlog": 0, "ready": 0, "active": 0, "blocked": 0, "completed": 0}
	args := scopeArgs
	where := `t.deleted_at IS NULL` + scopeSQL
	rows, err := s.DB.QueryContext(ctx, `SELECT c.semantic_state, COUNT(1) FROM tasks t JOIN columns c ON c.id=t.column_id WHERE `+where+` GROUP BY c.semantic_state`, args...)
	if err != nil {
		return roadmap, err
	}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			rows.Close()
			return roadmap, err
		}
		roadmap.StateCounts[state] = count
		roadmap.TaskTotals += count
	}
	rows.Close()
	roadmap.Completed = roadmap.StateCounts["completed"]
	if roadmap.TaskTotals > 0 {
		roadmap.CompletionPercent = float64(roadmap.Completed) * 100 / float64(roadmap.TaskTotals)
	}
	roadmap.TotalTasks = roadmap.TaskTotals
	roadmap.CompletedTasks = roadmap.Completed
	roadmap.CompletionPercentage = roadmap.CompletionPercent
	nowTime := time.Now().UTC()
	nowValue := nowTime.Format(time.RFC3339Nano)
	// "Due soon" is the next seven calendar days throughout the UI and API.
	soonValue := nowTime.Add(7 * 24 * time.Hour).Format(time.RFC3339Nano)
	args = append([]any{nowValue}, scopeArgs...)
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks t WHERE t.deleted_at IS NULL AND t.due_at IS NOT NULL AND t.due_at < ? AND t.completed_at IS NULL`+scopeSQL, args...).Scan(&roadmap.Overdue); err != nil {
		return roadmap, err
	}
	args = append([]any{nowValue, soonValue}, scopeArgs...)
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks t WHERE t.deleted_at IS NULL AND t.due_at IS NOT NULL AND t.due_at >= ? AND t.due_at <= ? AND t.completed_at IS NULL`+scopeSQL, args...).Scan(&roadmap.DueSoon); err != nil {
		return roadmap, err
	}

	query := `SELECT ` + taskColumns + ` FROM tasks t WHERE t.deleted_at IS NULL AND t.due_at IS NOT NULL AND t.completed_at IS NULL`
	query += scopeSQL
	args = scopeArgs
	query += ` ORDER BY t.due_at, t.id LIMIT 10`
	rows, err = s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return roadmap, err
	}
	for rows.Next() {
		task, err := taskFromRow(rows)
		if err != nil {
			rows.Close()
			return roadmap, err
		}
		roadmap.Upcoming = append(roadmap.Upcoming, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return roadmap, err
	}
	rows.Close()
	if roadmap.Upcoming == nil {
		roadmap.Upcoming = []Task{}
	}
	roadmap.UpcomingTasks = roadmap.Upcoming
	roadmap.OverdueCount = roadmap.Overdue
	roadmap.DueSoonCount = roadmap.DueSoon
	for i := range roadmap.Upcoming {
		if err := s.enrichTaskAt(ctx, &roadmap.Upcoming[i], nowTime); err != nil {
			return roadmap, err
		}
	}
	if err := s.populateTaskDependencySummaries(ctx, roadmap.Upcoming); err != nil {
		return roadmap, err
	}
	if err := s.populateTaskReleaseReferences(ctx, roadmap.Upcoming); err != nil {
		return roadmap, err
	}
	events, err := s.ListRecentEventsScoped(ctx, projectID, projectIDs, 10)
	if err != nil {
		return roadmap, err
	}
	roadmap.RecentActivity = events
	if roadmap.RecentActivity == nil {
		roadmap.RecentActivity = []Event{}
	}
	if projectID != "" {
		project, err := s.GetProject(ctx, projectID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return roadmap, err
		} else if err == nil {
			roadmap.Project = &project
		}
	} else {
		projects, err := s.ListProjectsFiltered(ctx, 200, 0, ProjectFilter{ProjectIDs: projectIDs})
		if err != nil {
			return roadmap, err
		}
		roadmap.Projects = make([]map[string]any, 0, len(projects))
		for _, project := range projects {
			roadmap.Projects = append(roadmap.Projects, map[string]any{
				"project":         project,
				"total_tasks":     project.TaskCount,
				"completed_tasks": project.CompletedCount,
				"completion_percentage": func() float64 {
					if project.TaskCount == 0 {
						return 0
					}
					return float64(project.CompletedCount) * 100 / float64(project.TaskCount)
				}(),
			})
		}
	}
	return roadmap, nil
}
