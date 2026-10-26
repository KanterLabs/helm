package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
)

func executePortableImportPlan(ctx context.Context, tx *sql.Tx, plan *portableImportPlan) error {
	for _, project := range plan.projects {
		if !project.create {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO projects(id,key,slug,name,description,color,favorite,archived_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, project.id, project.key, project.slug, project.source.Name, project.source.Description, project.source.Color, boolInt(project.source.Favorite), portableStringArg(project.source.ArchivedAt), project.source.CreatedAt, project.source.UpdatedAt)
		if err != nil {
			return err
		}
	}
	for _, column := range plan.columns {
		if !column.create {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO columns(id,project_id,name,semantic_state,position,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, column.id, column.projectID, column.source.Name, column.source.SemanticState, column.position, column.source.CreatedAt, column.source.UpdatedAt)
		if err != nil {
			return err
		}
	}
	for _, release := range plan.releases {
		if !release.create {
			continue
		}
		// A released release cannot accept task members through the migration's
		// direct SQL guard. Stage it as planned while its tasks/relationships are
		// inserted, then restore released_at after the complete subtree exists.
		_, err := tx.ExecContext(ctx, `INSERT INTO releases(id,project_id,name,description,target_date,released_at,released_by,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, release.id, release.projectID, release.source.Name, release.source.Description, portableStringArg(release.source.TargetDate), nil, portableStringArg(release.releasedBy), release.source.Version, release.source.CreatedAt, release.source.UpdatedAt)
		if err != nil {
			return err
		}
	}
	for _, label := range plan.labels {
		if !label.create {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO labels(id,project_id,name,color,created_at,updated_at) VALUES (?,?,?,?,?,?)`, label.id, label.projectID, label.source.Name, label.source.Color, label.source.CreatedAt, label.source.UpdatedAt)
		if err != nil {
			return err
		}
	}
	for _, task := range plan.tasks {
		if !task.create {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO tasks(id,project_id,number,column_id,kind,title,description,priority,position,assignee_id,claimed_by,claim_expires_at,due_at,version,completed_at,release_id,deleted_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,?,?)`, task.id, task.projectID, task.number, task.columnID, task.source.Kind, task.source.Title, task.source.Description, task.source.Priority, task.source.Position, portableStringArg(task.assigneeID), portableStringArg(task.claimedBy), portableStringArg(task.claimExpiresAt), portableStringArg(task.source.DueAt), task.source.Version, portableStringArg(task.source.CompletedAt), portableStringArg(task.source.ReleaseID), task.source.CreatedAt, task.source.UpdatedAt)
		if err != nil {
			return err
		}
	}
	// Bug details reference tasks, including duplicate_of, so insert them only
	// after all task rows exist. This preserves the complete typed-task state.
	for _, task := range plan.tasks {
		if !task.create || task.source.Bug == nil {
			continue
		}
		bug := task.source.Bug
		_, err := tx.ExecContext(ctx, `INSERT INTO bug_details(task_id,reporter_id,severity,actual_behavior,expected_behavior,reproduction_steps,environment,affected_version,resolution,resolved_by,resolved_at,duplicate_of) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, task.id, bug.ReporterID, portableStringArg(bug.Severity), bug.ActualBehavior, bug.ExpectedBehavior, bug.ReproductionSteps, bug.Environment, bug.AffectedVersion, portableStringArg(bug.Resolution), portableStringArg(bug.ResolvedBy), portableStringArg(bug.ResolvedAt), portableStringArg(bug.DuplicateOf))
		if err != nil {
			return err
		}
	}
	for _, relation := range plan.taskLabels {
		_, err := tx.ExecContext(ctx, `INSERT INTO task_labels(task_id,label_id) VALUES (?,?)`, relation.TaskID, relation.LabelID)
		if err != nil {
			return err
		}
	}
	for _, relation := range plan.dependencies {
		if !relation.create {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO task_dependencies(task_id,prerequisite_task_id,created_by,created_at) VALUES (?,?,?,?)`, relation.taskID, relation.prerequisiteID, portableStringArg(relation.createdBy), relation.source.CreatedAt)
		if err != nil {
			return err
		}
	}
	for _, relation := range plan.links {
		if !relation.create {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO task_links(source_task_id,target_task_id,link_type,created_at) VALUES (?,?,?,?)`, relation.sourceTaskID, relation.targetTaskID, relation.source.LinkType, relation.source.CreatedAt)
		if err != nil {
			return err
		}
	}
	for _, comment := range plan.comments {
		if !comment.create {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO comments(id,task_id,actor_id,body,created_at,updated_at) VALUES (?,?,?,?,?,?)`, comment.id, comment.taskID, comment.actorID, comment.source.Body, comment.source.CreatedAt, comment.source.UpdatedAt)
		if err != nil {
			return err
		}
	}
	for _, note := range plan.agentNotes {
		if !note.create {
			continue
		}
		evidence, err := json.Marshal(note.source.Evidence)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_notes(id,task_id,actor_id,category,body,evidence_json,version,created_at,updated_at,resolved_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, note.id, note.taskID, note.actorID, note.source.Category, note.source.Body, string(evidence), note.source.Version, note.source.CreatedAt, note.source.UpdatedAt, portableStringArg(note.source.ResolvedAt))
		if err != nil {
			return err
		}
	}
	for _, event := range plan.events {
		var cursor int64
		if event.create {
			result, err := tx.ExecContext(ctx, `INSERT INTO events(id,type,actor_id,project_id,task_id,payload,created_at) VALUES (?,?,?,?,?,?,?)`, event.id, event.source.Type, portableStringArg(event.actorID), portableStringArg(event.projectID), portableStringArg(event.taskID), string(event.source.Payload), event.source.CreatedAt)
			if err != nil {
				return err
			}
			cursor, err = result.LastInsertId()
			if err != nil {
				return err
			}
		} else {
			if err := tx.QueryRowContext(ctx, `SELECT cursor FROM events WHERE id=?`, event.id).Scan(&cursor); err != nil {
				return err
			}
		}
		if event.source.Cursor > 0 {
			plan.eventCursorMap[event.source.Cursor] = cursor
			if event.source.Cursor != cursor {
				portableAddRemap(&plan.report, "event", strconv.FormatInt(event.source.Cursor, 10), strconv.FormatInt(cursor, 10), "cursor", "event cursors are local to the destination SQLite instance")
				plan.report.Warnings = append(plan.report.Warnings, fmt.Sprintf("event %s cursor %d was remapped to destination cursor %d", event.source.ID, event.source.Cursor, cursor))
			}
		}
	}
	for _, work := range plan.work {
		if !work.create {
			continue
		}
		refs, err := portableMarshalRefs(work.source.CheckpointRefs)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO task_agent_work(task_id,operation_id,actor_id,state,phase,summary,next_action,checkpoint_refs,checkpoint_completed,checkpoint_total,started_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, work.taskID, work.source.OperationID, work.actorID, work.source.State, work.source.Phase, work.source.Summary, work.source.NextAction, refs, nullableIntArg(work.source.CheckpointCompleted), nullableIntArg(work.source.CheckpointTotal), work.source.StartedAt, work.source.UpdatedAt)
		if err != nil {
			return err
		}
	}
	for _, history := range plan.history {
		if !history.create {
			continue
		}
		refs, err := portableMarshalRefs(history.source.CheckpointRefs)
		if err != nil {
			return err
		}
		var progress any
		if history.source.ProgressEventCursor != nil {
			if mapped, ok := plan.eventCursorMap[*history.source.ProgressEventCursor]; ok {
				progress = mapped
			} else {
				portableAddRemap(&plan.report, "agent_work_history", strconv.FormatInt(*history.source.ProgressEventCursor, 10), "", "progress_event_cursor", "referenced event cursor was not imported")
				plan.report.Warnings = append(plan.report.Warnings, fmt.Sprintf("agent work history %s references event cursor %d that was not imported; reference cleared", history.source.ID, *history.source.ProgressEventCursor))
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO task_agent_work_history(id,task_id,operation_id,actor_id,state,phase,summary,next_action,checkpoint_refs,checkpoint_completed,checkpoint_total,started_at,created_at,generated_comment_id,progress_event_cursor) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, history.id, history.taskID, history.source.OperationID, history.actorID, history.source.State, history.source.Phase, history.source.Summary, history.source.NextAction, refs, nullableIntArg(history.source.CheckpointCompleted), nullableIntArg(history.source.CheckpointTotal), history.source.StartedAt, history.source.CreatedAt, portableStringArg(history.generatedCommentID), progress)
		if err != nil {
			return err
		}
	}
	for _, release := range plan.releases {
		if !release.create || release.source.ReleasedAt == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE releases SET released_at=? WHERE id=? AND released_at IS NULL`, *release.source.ReleasedAt, release.id); err != nil {
			return err
		}
	}
	if err := bumpPortableTaskCollectionRevisions(ctx, tx, plan); err != nil {
		return err
	}
	// CreateProject initializes a counter. Imported task numbers may be sparse,
	// so advance each affected project's counter without ever decreasing it.
	for _, project := range plan.projects {
		var next int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(number),0)+1 FROM tasks WHERE project_id=?`, project.id).Scan(&next); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_counters(project_id,next_number) VALUES (?,?) ON CONFLICT(project_id) DO UPDATE SET next_number=CASE WHEN excluded.next_number>project_counters.next_number THEN excluded.next_number ELSE project_counters.next_number END`, project.id, next); err != nil {
			return err
		}
	}
	return nil
}
