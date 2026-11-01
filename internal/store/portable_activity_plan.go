package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func portableWorkExists(q portableSQL, taskID string) (bool, error) {
	var exists int
	err := q.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM task_agent_work WHERE task_id=?)`, taskID).Scan(&exists)
	return exists != 0, err
}

func portableExistingWork(q portableSQL, taskID string) (PortableAgentWork, bool, error) {
	var work PortableAgentWork
	var refs string
	var completed, total sql.NullInt64
	err := q.QueryRowContext(context.Background(), `SELECT task_id,operation_id,actor_id,state,phase,summary,next_action,checkpoint_refs,checkpoint_completed,checkpoint_total,started_at,updated_at FROM task_agent_work WHERE task_id=?`, taskID).Scan(&work.TaskID, &work.OperationID, &work.ActorID, &work.State, &work.Phase, &work.Summary, &work.NextAction, &refs, &completed, &total, &work.StartedAt, &work.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableAgentWork{}, false, nil
	}
	if err != nil {
		return PortableAgentWork{}, false, err
	}
	if err := json.Unmarshal([]byte(refs), &work.CheckpointRefs); err != nil {
		return PortableAgentWork{}, false, err
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
	return work, true, nil
}

func portableWorkFieldsEqual(a, b PortableAgentWork) bool {
	return a.TaskID == b.TaskID && a.OperationID == b.OperationID && a.ActorID == b.ActorID && a.State == b.State && a.Phase == b.Phase && a.Summary == b.Summary && a.NextAction == b.NextAction && strings.Join(a.CheckpointRefs, "\x00") == strings.Join(b.CheckpointRefs, "\x00") && intPointerValue(a.CheckpointCompleted) == intPointerValue(b.CheckpointCompleted) && intPointerValue(a.CheckpointTotal) == intPointerValue(b.CheckpointTotal) && a.StartedAt == b.StartedAt && a.UpdatedAt == b.UpdatedAt
}

func portableHistoryExists(q portableSQL, id string) (bool, error) {
	var exists int
	err := q.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM task_agent_work_history WHERE id=?)`, id).Scan(&exists)
	return exists != 0, err
}

func portableExistingHistory(q portableSQL, id string) (PortableAgentWorkHistory, bool, error) {
	var history PortableAgentWorkHistory
	var refs string
	var completed, total sql.NullInt64
	var generatedComment sql.NullString
	var progress sql.NullInt64
	err := q.QueryRowContext(context.Background(), `SELECT id,task_id,operation_id,actor_id,state,phase,summary,next_action,checkpoint_refs,checkpoint_completed,checkpoint_total,started_at,created_at,generated_comment_id,progress_event_cursor FROM task_agent_work_history WHERE id=?`, id).Scan(&history.ID, &history.TaskID, &history.OperationID, &history.ActorID, &history.State, &history.Phase, &history.Summary, &history.NextAction, &refs, &completed, &total, &history.StartedAt, &history.CreatedAt, &generatedComment, &progress)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableAgentWorkHistory{}, false, nil
	}
	if err != nil {
		return PortableAgentWorkHistory{}, false, err
	}
	if err := json.Unmarshal([]byte(refs), &history.CheckpointRefs); err != nil {
		return PortableAgentWorkHistory{}, false, err
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
	if progress.Valid {
		value := progress.Int64
		history.ProgressEventCursor = &value
	}
	return history, true, nil
}

func portableHistoryFieldsEqual(a, b PortableAgentWorkHistory) bool {
	return a.ID == b.ID && a.TaskID == b.TaskID && a.OperationID == b.OperationID && a.ActorID == b.ActorID && a.State == b.State && a.Phase == b.Phase && a.Summary == b.Summary && a.NextAction == b.NextAction && strings.Join(a.CheckpointRefs, "\x00") == strings.Join(b.CheckpointRefs, "\x00") && intPointerValue(a.CheckpointCompleted) == intPointerValue(b.CheckpointCompleted) && intPointerValue(a.CheckpointTotal) == intPointerValue(b.CheckpointTotal) && a.StartedAt == b.StartedAt && a.CreatedAt == b.CreatedAt && stringPointerValue(a.GeneratedCommentID) == stringPointerValue(b.GeneratedCommentID) && int64PointerValue(a.ProgressEventCursor) == int64PointerValue(b.ProgressEventCursor)
}

func portableHistoryFieldsEqualIgnoringID(a, b PortableAgentWorkHistory) bool {
	a.ID, b.ID = "", ""
	return portableHistoryFieldsEqual(a, b)
}

// portableValidateActivityPlan mirrors the foreign-key and CHECK boundaries
// of the activity tables before the write transaction reaches execution. The
// task/comment rows may themselves be part of this import and therefore are
// considered planned targets. This keeps a dry-run report and a real import
// on the same validation path instead of discovering an agent-work-history
// failure halfway through an otherwise valid archive.
func portableValidateActivityPlan(ctx context.Context, q portableSQL, plan *portableImportPlan) error {
	plannedTasks := map[string]struct{}{}
	for _, task := range plan.tasks {
		if task.create {
			plannedTasks[task.id] = struct{}{}
		}
	}
	plannedComments := map[string]struct{}{}
	for _, comment := range plan.comments {
		if comment.create {
			plannedComments[comment.id] = struct{}{}
		}
	}
	plannedEventCursors := map[int64]struct{}{}
	for _, event := range plan.events {
		if event.create && event.source.Cursor > 0 {
			plannedEventCursors[event.source.Cursor] = struct{}{}
		}
	}
	existsID := func(table, column, value string) (bool, error) {
		// table and column are selected from the fixed call sites below; neither
		// receives archive/user input.
		var exists int
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE `+column+`=?)`, value).Scan(&exists); err != nil {
			return false, err
		}
		return exists != 0, nil
	}
	validTask := func(taskID string) (bool, error) {
		if _, planned := plannedTasks[taskID]; planned {
			return true, nil
		}
		return existsID("tasks", "id", taskID)
	}
	validActor := func(actorID string) (bool, error) {
		return existsID("actors", "id", actorID)
	}
	for _, work := range plan.work {
		if !work.create {
			continue
		}
		if valid, err := validTask(work.taskID); err != nil {
			return err
		} else if !valid {
			addPortableIssue(&plan.report, "agent_work", work.source.TaskID, "task_id", "task target is not available for the activity snapshot")
		}
		if valid, err := validActor(work.actorID); err != nil {
			return err
		} else if !valid {
			addPortableIssue(&plan.report, "agent_work", work.source.TaskID, "actor_id", "actor target is not available for the activity snapshot")
		}
		if _, err := portableMarshalRefs(work.source.CheckpointRefs); err != nil {
			addPortableIssue(&plan.report, "agent_work", work.source.TaskID, "checkpoint_refs", "checkpoint_refs cannot be encoded as a JSON array")
		}
	}
	for _, history := range plan.history {
		if !history.create {
			continue
		}
		if valid, err := validTask(history.taskID); err != nil {
			return err
		} else if !valid {
			addPortableIssue(&plan.report, "agent_work_history", history.source.ID, "task_id", "task target is not available for the activity history row")
		}
		if valid, err := validActor(history.actorID); err != nil {
			return err
		} else if !valid {
			addPortableIssue(&plan.report, "agent_work_history", history.source.ID, "actor_id", "actor target is not available for the activity history row")
		}
		if _, err := portableMarshalRefs(history.source.CheckpointRefs); err != nil {
			addPortableIssue(&plan.report, "agent_work_history", history.source.ID, "checkpoint_refs", "checkpoint_refs cannot be encoded as a JSON array")
		}
		if history.generatedCommentID != nil {
			if _, planned := plannedComments[*history.generatedCommentID]; !planned {
				valid, err := existsID("comments", "id", *history.generatedCommentID)
				if err != nil {
					return err
				}
				if !valid {
					addPortableIssue(&plan.report, "agent_work_history", history.source.ID, "generated_comment_id", "generated comment target is not available for the activity history row")
				}
			}
		}
		if history.source.ProgressEventCursor != nil {
			cursor := *history.source.ProgressEventCursor
			if _, planned := plannedEventCursors[cursor]; !planned {
				if _, mapped := plan.eventCursorMap[cursor]; !mapped {
					// Execution deliberately clears an event cursor that was not
					// included in the archive. It is not a foreign-key violation;
					// the write path will report the explicit remap/warning.
					continue
				}
			}
		}
	}
	return nil
}

func intPointerValue(value *int) int {
	if value == nil {
		return -1
	}
	return *value
}

func int64PointerValue(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
}
