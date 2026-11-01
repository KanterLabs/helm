package store

import (
	"context"
	"database/sql"
)

// Resolve parent edges only after all task/bug remaps have stabilized. Reused
// task rows are immutable; mismatched hierarchy is a conflict, never an update.
func planPortableMetadata(ctx context.Context, q portableSQL, plan *portableImportPlan, actorMap map[string]string) error {
	if plan.archive.Version < PortableVersionCurrent {
		return nil
	}
	plan.checklistMap = map[string]string{}
	used, err := portableIDSet(q, "task_checklist_items")
	if err != nil {
		return err
	}
	for i := range plan.tasks {
		task := &plan.tasks[i]
		if task.source.ParentTaskID != nil {
			mapped := plan.taskMap[*task.source.ParentTaskID]
			task.source.ParentTaskID = &mapped
		}
		if !task.create {
			existing, _, err := portableExistingTask(q, task.id)
			if err != nil {
				return err
			}
			if stringPointerValue(existing.ParentTaskID) != stringPointerValue(task.source.ParentTaskID) {
				addPortableIssue(&plan.report, "task", task.source.ID, "parent_task_id", "reused destination task has different parent; destination rows cannot be changed")
			}
			for j, item := range task.source.Checklist {
				target := existing.Checklist[j].ID
				plan.checklistMap[item.ID] = target
				if item.ID != target {
					portableAddRemap(&plan.report, "checklist", item.ID, target, "id", "reused checklist remap from earlier import")
				}
			}
			continue
		}
		for j := range task.source.Checklist {
			item := &task.source.Checklist[j]
			original := item.ID
			if _, exists := used[item.ID]; exists {
				if plan.options.Conflict == portableConflictFail {
					return portableConflictError(plan.report, "checklist id conflict")
				}
				item.ID = portableCandidateID("checklist", original, used)
				portableAddRemap(&plan.report, "checklist", original, item.ID, "id", "source id conflicted with destination checklist")
			}
			plan.checklistMap[original] = item.ID
			used[item.ID] = struct{}{}
		}
	}
	newChildren := map[string]int{}
	for _, task := range plan.tasks {
		if task.create && task.source.ParentTaskID != nil {
			newChildren[*task.source.ParentTaskID]++
		}
	}
	for parent, added := range newChildren {
		var existing int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE parent_task_id=? AND deleted_at IS NULL`, parent).Scan(&existing); err != nil {
			return err
		}
		if existing+added > MaxTaskHierarchyChildren {
			addPortableIssue(&plan.report, "task", parent, "parent_task_id", "destination parent would exceed 200 direct children")
		}
	}
	return nil
}

func executePortableMetadata(ctx context.Context, tx *sql.Tx, plan *portableImportPlan) error {
	if plan.archive.Version < PortableVersionCurrent {
		return nil
	}
	// Insert tasks with no parent first, then set parent edges from roots down.
	// This supports archives whose children precede their parents and ensures
	// existing SQL depth/cycle/project guards validate the complete final chain.
	remaining := map[string]*portableTaskPlan{}
	for i := range plan.tasks {
		task := &plan.tasks[i]
		if task.create && task.source.ParentTaskID != nil {
			remaining[task.id] = task
		}
	}
	for len(remaining) > 0 {
		for id, task := range remaining {
			if _, pending := remaining[*task.source.ParentTaskID]; pending {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET parent_task_id=? WHERE id=?`, *task.source.ParentTaskID, id); err != nil {
				return err
			}
			delete(remaining, id)
		}
	}
	for _, task := range plan.tasks {
		if !task.create {
			continue
		}
		for _, item := range task.source.Checklist {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_checklist_items(id,task_id,text,position,completed,completed_at,completed_by,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`, item.ID, task.id, item.Text, item.Position, boolInt(item.Completed), portableStringArg(item.CompletedAt), portableStringArg(item.CompletedBy), item.CreatedAt, item.UpdatedAt); err != nil {
				return err
			}
		}
	}
	return nil
}
