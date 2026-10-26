package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
)

func exportPortableRelated(ctx context.Context, q portableSQL, archive PortableArchive, projectIDs []string, taskSet, labelSet, actorSet map[string]struct{}) (PortableArchive, error) {
	where, args := portableIDsClause("t.project_id", projectIDs)
	rows, err := q.QueryContext(ctx, `SELECT tl.task_id,tl.label_id FROM task_labels tl JOIN tasks t ON t.id=tl.task_id WHERE t.deleted_at IS NULL`+where+` ORDER BY tl.task_id,tl.label_id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	for rows.Next() {
		var relation PortableTaskLabel
		if err := rows.Scan(&relation.TaskID, &relation.LabelID); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		if _, ok := taskSet[relation.TaskID]; ok {
			if _, ok := labelSet[relation.LabelID]; ok {
				archive.Relationships.TaskLabels = append(archive.Relationships.TaskLabels, relation)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	where, args = portableIDsClause("t.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT td.task_id,td.prerequisite_task_id,td.created_by,td.created_at FROM task_dependencies td JOIN tasks t ON t.id=td.task_id WHERE t.deleted_at IS NULL`+where+` ORDER BY td.task_id,td.prerequisite_task_id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	for rows.Next() {
		var relation PortableDependency
		var actor sql.NullString
		if err := rows.Scan(&relation.TaskID, &relation.PrerequisiteID, &actor, &relation.CreatedAt); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		relation.CreatedBy = nullableString(actor)
		if relation.CreatedBy != nil {
			actorSet[*relation.CreatedBy] = struct{}{}
		}
		if _, ok := taskSet[relation.TaskID]; ok {
			if _, ok := taskSet[relation.PrerequisiteID]; ok {
				archive.Relationships.Dependencies = append(archive.Relationships.Dependencies, relation)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	where, args = portableIDsClause("source.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT link.source_task_id,link.target_task_id,link.link_type,link.created_at FROM task_links link JOIN tasks source ON source.id=link.source_task_id WHERE source.deleted_at IS NULL`+where+` ORDER BY link.source_task_id,link.target_task_id,link.link_type`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	for rows.Next() {
		var relation PortableTaskLink
		if err := rows.Scan(&relation.SourceTaskID, &relation.TargetTaskID, &relation.LinkType, &relation.CreatedAt); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		if _, ok := taskSet[relation.SourceTaskID]; ok {
			if _, ok := taskSet[relation.TargetTaskID]; ok {
				archive.Relationships.TaskLinks = append(archive.Relationships.TaskLinks, relation)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	where, args = portableIDsClause("t.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT c.id,c.task_id,c.actor_id,c.body,c.created_at,c.updated_at FROM comments c JOIN tasks t ON t.id=c.task_id WHERE t.deleted_at IS NULL`+where+` ORDER BY c.task_id,c.created_at,c.id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	for rows.Next() {
		var comment PortableComment
		if err := rows.Scan(&comment.ID, &comment.TaskID, &comment.ActorID, &comment.Body, &comment.CreatedAt, &comment.UpdatedAt); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		archive.Comments = append(archive.Comments, comment)
		actorSet[comment.ActorID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	where, args = portableIDsClause("t.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT n.id,n.task_id,n.actor_id,n.category,n.body,n.evidence_json,n.version,n.created_at,n.updated_at,n.resolved_at FROM agent_notes n JOIN tasks t ON t.id=n.task_id WHERE t.deleted_at IS NULL`+where+` ORDER BY n.task_id,n.created_at,n.id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	for rows.Next() {
		var note PortableAgentNote
		var evidence string
		var resolved sql.NullString
		if err := rows.Scan(&note.ID, &note.TaskID, &note.ActorID, &note.Category, &note.Body, &evidence, &note.Version, &note.CreatedAt, &note.UpdatedAt, &resolved); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		if err := json.Unmarshal([]byte(evidence), &note.Evidence); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		note.ResolvedAt = nullableString(resolved)
		archive.AgentNotes = append(archive.AgentNotes, note)
		actorSet[note.ActorID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	where, args = portableIDsClause("e.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT e.cursor,e.id,e.type,e.actor_id,e.project_id,e.task_id,e.payload,e.created_at FROM events e WHERE e.project_id IS NOT NULL AND (e.task_id IS NULL OR EXISTS (SELECT 1 FROM tasks live_task WHERE live_task.id=e.task_id AND live_task.deleted_at IS NULL))`+where+` ORDER BY e.cursor`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	for rows.Next() {
		var event PortableEvent
		var actor, project, task, payload sql.NullString
		if err := rows.Scan(&event.Cursor, &event.ID, &event.Type, &actor, &project, &task, &payload, &event.CreatedAt); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		event.ActorID, event.ProjectID, event.TaskID = nullableString(actor), nullableString(project), nullableString(task)
		if payload.Valid {
			event.Payload = json.RawMessage(payload.String)
		} else {
			event.Payload = json.RawMessage(`{}`)
		}
		archive.Activity.Events = append(archive.Activity.Events, event)
		if event.ActorID != nil {
			actorSet[*event.ActorID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	where, args = portableIDsClause("t.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT aw.task_id,aw.operation_id,aw.actor_id,aw.state,aw.phase,aw.summary,aw.next_action,aw.checkpoint_refs,aw.checkpoint_completed,aw.checkpoint_total,aw.started_at,aw.updated_at FROM task_agent_work aw JOIN tasks t ON t.id=aw.task_id WHERE t.deleted_at IS NULL`+where+` ORDER BY aw.task_id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	for rows.Next() {
		var work PortableAgentWork
		var refs string
		var completed, total sql.NullInt64
		if err := rows.Scan(&work.TaskID, &work.OperationID, &work.ActorID, &work.State, &work.Phase, &work.Summary, &work.NextAction, &refs, &completed, &total, &work.StartedAt, &work.UpdatedAt); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		if err := json.Unmarshal([]byte(refs), &work.CheckpointRefs); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		if work.CheckpointRefs == nil {
			work.CheckpointRefs = []string{}
		}
		if completed.Valid {
			value := int(completed.Int64)
			work.CheckpointCompleted = &value
		}
		if total.Valid {
			value := int(total.Int64)
			work.CheckpointTotal = &value
		}
		archive.Activity.AgentWork = append(archive.Activity.AgentWork, work)
		actorSet[work.ActorID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	where, args = portableIDsClause("t.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT history.id,history.task_id,history.operation_id,history.actor_id,history.state,history.phase,history.summary,history.next_action,history.checkpoint_refs,history.checkpoint_completed,history.checkpoint_total,history.started_at,history.created_at,history.generated_comment_id,history.progress_event_cursor FROM task_agent_work_history history JOIN tasks t ON t.id=history.task_id WHERE t.deleted_at IS NULL`+where+` ORDER BY history.task_id,history.created_at,history.id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	for rows.Next() {
		var history PortableAgentWorkHistory
		var refs string
		var completed, total, eventCursor sql.NullInt64
		var generatedComment sql.NullString
		if err := rows.Scan(&history.ID, &history.TaskID, &history.OperationID, &history.ActorID, &history.State, &history.Phase, &history.Summary, &history.NextAction, &refs, &completed, &total, &history.StartedAt, &history.CreatedAt, &generatedComment, &eventCursor); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		if err := json.Unmarshal([]byte(refs), &history.CheckpointRefs); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		if history.CheckpointRefs == nil {
			history.CheckpointRefs = []string{}
		}
		if completed.Valid {
			value := int(completed.Int64)
			history.CheckpointCompleted = &value
		}
		if total.Valid {
			value := int(total.Int64)
			history.CheckpointTotal = &value
		}
		if generatedComment.Valid {
			history.GeneratedCommentID = &generatedComment.String
		}
		if eventCursor.Valid {
			value := eventCursor.Int64
			history.ProgressEventCursor = &value
		}
		archive.Activity.AgentWorkHistory = append(archive.Activity.AgentWorkHistory, history)
		actorSet[history.ActorID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	// Include safe actor references so an import can explain missing identity
	// mappings without exporting any authentication material.
	actorIDs := make([]string, 0, len(actorSet))
	for id := range actorSet {
		actorIDs = append(actorIDs, id)
	}
	sort.Strings(actorIDs)
	for _, id := range actorIDs {
		var actor PortableActorReference
		var disabled sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT id,kind,name,description,disabled_at FROM actors WHERE id=?`, id).Scan(&actor.ID, &actor.Kind, &actor.Name, &actor.Description, &disabled); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return PortableArchive{}, err
		}
		actor.DisabledAt = nullableString(disabled)
		archive.Actors = append(archive.Actors, actor)
	}
	return archive, nil
}
