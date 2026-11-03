package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

func (s *Store) listProjectTimelineHistory(ctx context.Context, projectID string, boundary timelineSortKey, limit int) ([]timelineHistoryRow, error) {
	predicate, predicateArgs := timelineKeysetPredicate("created_at", "timeline_event_cursor", "id", "agent_progress", boundary)
	query := `WITH source AS (
		SELECT h.id, h.task_id, h.operation_id, h.actor_id, h.state, h.phase, h.summary,
			h.next_action, h.checkpoint_refs, h.checkpoint_completed, h.checkpoint_total,
			h.started_at, h.created_at, h.generated_comment_id, h.progress_event_cursor,
			COALESCE(h.progress_event_cursor, 0) AS timeline_event_cursor,
			a.kind AS actor_kind, a.name AS actor_name
		FROM task_agent_work_history h
		JOIN tasks t ON t.id=h.task_id
		LEFT JOIN actors a ON a.id=h.actor_id
		WHERE t.project_id=? AND t.deleted_at IS NULL
	)
	SELECT id, task_id, operation_id, actor_id, state, phase, summary, next_action,
		checkpoint_refs, checkpoint_completed, checkpoint_total, started_at, created_at,
		generated_comment_id, progress_event_cursor, timeline_event_cursor, actor_kind, actor_name
	FROM source`
	args := []any{projectID}
	if predicate != "" {
		query += " WHERE " + predicate
		args = append(args, predicateArgs...)
	}
	query += ` ORDER BY created_at DESC, timeline_event_cursor DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]timelineHistoryRow, 0, limit)
	for rows.Next() {
		var row timelineHistoryRow
		if err := rows.Scan(&row.id, &row.taskID, &row.operationID, &row.actorID, &row.state, &row.phase, &row.summary, &row.nextAction, &row.refsJSON, &row.completed, &row.total, &row.startedAt, &row.createdAt, &row.generatedCommentID, &row.progressEventCursor, &row.timelineEventCursor, &row.actorKind, &row.actorName); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *Store) listProjectTimelineComments(ctx context.Context, projectID string, boundary timelineSortKey, limit int) ([]timelineCommentRow, error) {
	predicate, predicateArgs := timelineKeysetPredicate("created_at", "timeline_event_cursor", "id", "comment", boundary)
	query := `WITH source AS (
		SELECT c.id, c.task_id, c.actor_id, c.body, c.version, c.created_at, c.updated_at, c.deleted_at,
			COALESCE((
				SELECT MAX(ce.cursor)
				FROM events ce
				WHERE ce.task_id=c.task_id
				  AND ce.type='comment.created'
				  AND json_valid(ce.payload)
				  AND json_extract(ce.payload, '$.comment_id')=c.id
			), 0) AS timeline_event_cursor,
			a.id AS timeline_actor_id, a.kind AS timeline_actor_kind, a.name AS timeline_actor_name
		FROM comments c
		JOIN tasks t ON t.id=c.task_id
		LEFT JOIN actors a ON a.id=c.actor_id
		WHERE t.project_id=? AND t.deleted_at IS NULL
		  AND c.deleted_at IS NULL
		  AND NOT EXISTS (
			  SELECT 1 FROM task_agent_work_history h
			  WHERE h.generated_comment_id=c.id
		  )
	)
	SELECT id, task_id, actor_id, body, version, created_at, updated_at, deleted_at,
		timeline_event_cursor, timeline_actor_id, timeline_actor_kind, timeline_actor_name
	FROM source`
	args := []any{projectID}
	if predicate != "" {
		query += " WHERE " + predicate
		args = append(args, predicateArgs...)
	}
	query += ` ORDER BY created_at DESC, timeline_event_cursor DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]timelineCommentRow, 0, limit)
	for rows.Next() {
		var row timelineCommentRow
		var deletedAt sql.NullString
		if err := rows.Scan(&row.comment.ID, &row.comment.TaskID, &row.comment.ActorID, &row.comment.Body, &row.comment.Version, &row.comment.CreatedAt, &row.comment.UpdatedAt, &deletedAt, &row.timelineEventCursor, &row.actorID, &row.actorKind, &row.actorName); err != nil {
			return nil, err
		}
		row.comment.DeletedAt = nullableString(deletedAt)
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *Store) listProjectTimelineEvents(ctx context.Context, projectID string, boundary timelineSortKey, limit int) ([]timelineProjectEventRow, error) {
	predicate, predicateArgs := timelineKeysetPredicate("created_at", "cursor", "id", "task_change", boundary)
	query := `WITH source AS (
		SELECT e.cursor, e.id, e.type, e.actor_id, e.project_id, e.task_id, e.payload, e.created_at,
			a.id AS timeline_actor_id, a.kind AS timeline_actor_kind, a.name AS timeline_actor_name
		FROM events e
		JOIN tasks t ON t.id=e.task_id
		LEFT JOIN actors a ON a.id=e.actor_id
		WHERE t.project_id=? AND t.deleted_at IS NULL
		  AND NOT EXISTS (
			  SELECT 1 FROM task_agent_work_history h
			  WHERE h.progress_event_cursor=e.cursor
		  )
		  AND NOT (
			  e.type='comment.created'
			  AND (
				  EXISTS (
					  SELECT 1 FROM comments c
					  WHERE c.id=CASE WHEN json_valid(e.payload) THEN json_extract(e.payload, '$.comment_id') END
						AND c.task_id=e.task_id
				  )
				  OR EXISTS (
					  SELECT 1 FROM task_agent_work_history h
					  WHERE h.generated_comment_id=CASE WHEN json_valid(e.payload) THEN json_extract(e.payload, '$.comment_id') END
						AND h.task_id=e.task_id
				  )
			  )
		  )
	)
	SELECT cursor, id, type, actor_id, project_id, task_id, payload, created_at,
		timeline_actor_id, timeline_actor_kind, timeline_actor_name
	FROM source`
	args := []any{projectID}
	if predicate != "" {
		query += " WHERE " + predicate
		args = append(args, predicateArgs...)
	}
	query += ` ORDER BY created_at DESC, cursor DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]timelineProjectEventRow, 0, limit)
	for rows.Next() {
		row, err := scanProjectTimelineEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *Store) listProjectTimelineCandidates(ctx context.Context, projectID string, filter TaskTimelineFilter, boundary timelineSortKey) ([]timelineCandidate, error) {
	sourceLimit := filter.Limit + 1
	candidates := make([]timelineCandidate, 0, sourceLimit*3)
	if filter.Kind == "" || filter.Kind == "agent_progress" {
		histories, err := s.listProjectTimelineHistory(ctx, projectID, boundary, sourceLimit)
		if err != nil {
			return nil, err
		}
		for _, row := range histories {
			candidate, err := timelineHistoryCandidate(row)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, candidate)
		}
	}
	if filter.Kind == "" || filter.Kind == "comment" {
		comments, err := s.listProjectTimelineComments(ctx, projectID, boundary, sourceLimit)
		if err != nil {
			return nil, err
		}
		for _, row := range comments {
			candidates = append(candidates, timelineCommentCandidate(row))
		}
	}
	if filter.Kind == "" || filter.Kind == "task_change" {
		events, err := s.listProjectTimelineEvents(ctx, projectID, boundary, sourceLimit)
		if err != nil {
			return nil, err
		}
		for _, row := range events {
			taskID := ""
			if row.event.TaskID != nil {
				taskID = *row.event.TaskID
			}
			candidates = append(candidates, timelineEventCandidate(row.event, row.actorID, row.actorKind, row.actorName, taskID))
		}
	}
	return candidates, nil
}

func scanProjectTimelineEvent(scanner interface{ Scan(...any) error }) (timelineProjectEventRow, error) {
	var row timelineProjectEventRow
	var actor, project, task, payload sql.NullString
	if err := scanner.Scan(&row.event.Cursor, &row.event.ID, &row.event.Type, &actor, &project, &task, &payload, &row.event.CreatedAt, &row.actorID, &row.actorKind, &row.actorName); err != nil {
		return timelineProjectEventRow{}, err
	}
	row.event.ActorID, row.event.ProjectID, row.event.TaskID = nullableString(actor), nullableString(project), nullableString(task)
	if payload.Valid {
		row.event.Payload = json.RawMessage(payload.String)
	} else {
		row.event.Payload = json.RawMessage(`{}`)
	}
	return row, nil
}
