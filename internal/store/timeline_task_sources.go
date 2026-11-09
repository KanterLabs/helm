package store

import (
	"context"
	"database/sql"
)

// timelineCapacity bounds slice preallocation. Page reads never exceed it;
// the unpaginated activity-signal reader grows its slices as needed.
func timelineCapacity(limit int) int {
	if limit > 201 {
		return 201
	}
	return limit
}

func (s *Store) listTaskTimelineCandidates(ctx context.Context, taskID string, filter TaskTimelineFilter, boundary timelineSortKey) ([]timelineCandidate, error) {
	return s.collectTaskTimelineCandidates(ctx, taskID, filter, boundary, nil)
}

// collectTaskTimelineCandidates reads the three task sources with the shared
// deduplication. With a nil undecodable callback a stored progress row that
// cannot be decoded fails the read, as the timeline endpoint always has.
// Otherwise the row is reported and kept as a bare agent_progress candidate
// (no Progress), so a whole-history reader never aborts on one bad row.
func (s *Store) collectTaskTimelineCandidates(ctx context.Context, taskID string, filter TaskTimelineFilter, boundary timelineSortKey, undecodable func(id string, err error)) ([]timelineCandidate, error) {
	sourceLimit := filter.Limit + 1
	candidates := make([]timelineCandidate, 0, timelineCapacity(sourceLimit)*3)
	if filter.Kind == "" || filter.Kind == "agent_progress" {
		histories, err := s.listTimelineHistory(ctx, taskID, boundary, sourceLimit)
		if err != nil {
			return nil, err
		}
		for _, row := range histories {
			candidate, err := timelineHistoryCandidate(row)
			if err != nil {
				if undecodable == nil {
					return nil, err
				}
				undecodable(row.id, err)
				item := TaskTimelineItem{ID: row.id, Kind: "agent_progress", TaskID: row.taskID, CreatedAt: row.createdAt}
				candidate = timelineCandidate{item: item, key: newTimelineSortKey(row.createdAt, item.Kind, item.ID, row.timelineEventCursor)}
			}
			candidates = append(candidates, candidate)
		}
	}
	if filter.Kind == "" || filter.Kind == "comment" {
		comments, err := s.listTimelineComments(ctx, taskID, boundary, sourceLimit)
		if err != nil {
			return nil, err
		}
		for _, row := range comments {
			candidates = append(candidates, timelineCommentCandidate(row))
		}
	}
	if filter.Kind == "" || filter.Kind == "task_change" {
		events, err := s.listTimelineEvents(ctx, taskID, boundary, sourceLimit)
		if err != nil {
			return nil, err
		}
		for _, row := range events {
			candidates = append(candidates, timelineEventCandidate(row.event, row.actorID, row.actorKind, row.actorName, taskID))
		}
	}
	return candidates, nil
}

func (s *Store) listTimelineHistory(ctx context.Context, taskID string, boundary timelineSortKey, limit int) ([]timelineHistoryRow, error) {
	predicate, predicateArgs := timelineKeysetPredicate("created_at", "timeline_event_cursor", "id", "agent_progress", boundary)
	query := `WITH source AS (
		SELECT h.id, h.task_id, h.operation_id, h.actor_id, h.state, h.phase, h.summary,
			h.next_action, h.checkpoint_refs, h.checkpoint_completed, h.checkpoint_total,
			h.started_at, h.created_at, h.generated_comment_id, h.progress_event_cursor,
			COALESCE(h.progress_event_cursor, 0) AS timeline_event_cursor,
			a.kind AS actor_kind, a.name AS actor_name
		FROM task_agent_work_history h
		LEFT JOIN actors a ON a.id=h.actor_id
		WHERE h.task_id=?
	)
	SELECT id, task_id, operation_id, actor_id, state, phase, summary, next_action,
		checkpoint_refs, checkpoint_completed, checkpoint_total, started_at, created_at,
		generated_comment_id, progress_event_cursor, timeline_event_cursor, actor_kind, actor_name
	FROM source`
	args := []any{taskID}
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
	result := make([]timelineHistoryRow, 0, timelineCapacity(limit))
	for rows.Next() {
		var row timelineHistoryRow
		if err := rows.Scan(&row.id, &row.taskID, &row.operationID, &row.actorID, &row.state, &row.phase, &row.summary, &row.nextAction, &row.refsJSON, &row.completed, &row.total, &row.startedAt, &row.createdAt, &row.generatedCommentID, &row.progressEventCursor, &row.timelineEventCursor, &row.actorKind, &row.actorName); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *Store) listTimelineComments(ctx context.Context, taskID string, boundary timelineSortKey, limit int) ([]timelineCommentRow, error) {
	predicate, predicateArgs := timelineKeysetPredicate("created_at", "timeline_event_cursor", "id", "comment", boundary)
	// The task's comment.created events are grouped once into created
	// (TC-380), so each comment costs one indexed lookup instead of a scan of
	// the task's events. CAST(... AS TEXT) is the conversion c.id's TEXT
	// affinity applied in the former per-comment json_extract(...)=c.id test,
	// so numeric and text payload ids still match the same comments.
	query := `WITH created AS MATERIALIZED (
		SELECT CAST(json_extract(ce.payload, '$.comment_id') AS TEXT) AS comment_id, MAX(ce.cursor) AS cursor
		FROM events ce
		WHERE ce.task_id=?
		  AND ce.type='comment.created'
		  AND json_valid(ce.payload)
		GROUP BY 1
	), source AS (
		SELECT c.id, c.task_id, c.actor_id, c.body, c.version, c.created_at, c.updated_at, c.deleted_at,
			COALESCE(cr.cursor, 0) AS timeline_event_cursor,
			a.id AS timeline_actor_id, a.kind AS timeline_actor_kind, a.name AS timeline_actor_name
		FROM comments c
		LEFT JOIN actors a ON a.id=c.actor_id
		LEFT JOIN created cr ON cr.comment_id=c.id
		WHERE c.task_id=?
		  AND c.deleted_at IS NULL
		  AND NOT EXISTS (
			  SELECT 1 FROM task_agent_work_history h
			  WHERE h.generated_comment_id=c.id
		  )
	)
	SELECT id, task_id, actor_id, body, version, created_at, updated_at, deleted_at,
		timeline_event_cursor, timeline_actor_id, timeline_actor_kind, timeline_actor_name
	FROM source`
	args := []any{taskID, taskID}
	if predicate != "" {
		query += " WHERE " + predicate
		args = append(args, predicateArgs...)
	}
	query += ` ORDER BY created_at DESC, timeline_event_cursor DESC, id DESC LIMIT ?`
	args = append(args, limit)
	s.observe(query, args)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]timelineCommentRow, 0, timelineCapacity(limit))
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

func (s *Store) listTimelineEvents(ctx context.Context, taskID string, boundary timelineSortKey, limit int) ([]timelineEventRow, error) {
	predicate, predicateArgs := timelineKeysetPredicate("created_at", "cursor", "id", "task_change", boundary)
	query := `WITH source AS (
		SELECT e.cursor, e.id, e.type, e.actor_id, e.project_id, e.task_id, e.payload, e.created_at,
			a.id AS timeline_actor_id, a.kind AS timeline_actor_kind, a.name AS timeline_actor_name
		FROM events e
		LEFT JOIN actors a ON a.id=e.actor_id
		WHERE e.task_id=?
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
	args := []any{taskID}
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
	result := make([]timelineEventRow, 0, timelineCapacity(limit))
	for rows.Next() {
		var row timelineEventRow
		event, err := scanEventWithActor(rows, &row.actorID, &row.actorKind, &row.actorName)
		if err != nil {
			return nil, err
		}
		row.event = event
		result = append(result, row)
	}
	return result, rows.Err()
}
