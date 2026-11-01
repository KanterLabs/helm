package store

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
)

// PortableChecklistItem preserves ordering, identity and completion attribution.
// Credentials and actor authentication records remain outside the archive.
type PortableChecklistItem struct {
	ID          string  `json:"id"`
	Text        string  `json:"text"`
	Position    int     `json:"position"`
	Completed   bool    `json:"completed"`
	CompletedAt *string `json:"completed_at,omitempty"`
	CompletedBy *string `json:"completed_by,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

const portableLegacyWarning = "portable versions 1-3 omit task checklists, parent hierarchy and project checklist completion policy; require safeguards cannot be reconstructed from this archive"

func portableProjectPolicy(p PortableProject) string {
	if p.ChecklistCompletionPolicy == "" {
		return "warn"
	}
	return p.ChecklistCompletionPolicy
}

func readPortableTaskMetadata(ctx context.Context, q portableSQL, task *PortableTask) error {
	var parent sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT parent_task_id FROM tasks WHERE id=?`, task.ID).Scan(&parent); err != nil {
		return err
	}
	task.ParentTaskID = nullableString(parent)
	rows, err := q.QueryContext(ctx, `SELECT id,text,position,completed,completed_at,completed_by,created_at,updated_at FROM task_checklist_items WHERE task_id=? ORDER BY position,id`, task.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	task.Checklist = []PortableChecklistItem{}
	for rows.Next() {
		var item PortableChecklistItem
		var completed int
		var at, by sql.NullString
		if err := rows.Scan(&item.ID, &item.Text, &item.Position, &completed, &at, &by, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return err
		}
		item.Completed = boolValue(completed)
		item.CompletedAt = nullableString(at)
		item.CompletedBy = nullableString(by)
		task.Checklist = append(task.Checklist, item)
	}
	return rows.Err()
}

func exportPortableMetadata(ctx context.Context, q portableSQL, archive *PortableArchive) error {
	for i := range archive.Projects {
		p := &archive.Projects[i]
		if err := q.QueryRowContext(ctx, `SELECT checklist_completion_policy FROM projects WHERE id=?`, p.ID).Scan(&p.ChecklistCompletionPolicy); err != nil {
			return err
		}
	}
	actorSet := map[string]bool{}
	for _, actor := range archive.Actors {
		actorSet[actor.ID] = true
	}
	for i := range archive.Tasks {
		if err := readPortableTaskMetadata(ctx, q, &archive.Tasks[i]); err != nil {
			return err
		}
		for _, item := range archive.Tasks[i].Checklist {
			if item.CompletedBy == nil || actorSet[*item.CompletedBy] {
				continue
			}
			var actor PortableActorReference
			var disabled sql.NullString
			if err := q.QueryRowContext(ctx, `SELECT id,kind,name,description,disabled_at FROM actors WHERE id=?`, *item.CompletedBy).Scan(&actor.ID, &actor.Kind, &actor.Name, &actor.Description, &disabled); err != nil {
				return err
			}
			actor.DisabledAt = nullableString(disabled)
			archive.Actors = append(archive.Actors, actor)
			actorSet[actor.ID] = true
		}
	}
	return nil
}

func portableOmitMetadata(archive *PortableArchive) {
	archive.Warnings = append(archive.Warnings, portableLegacyWarning)
	if archive.Version < 3 {
		archive.Warnings = append(archive.Warnings, "portable versions 1-2 omit GitHub PR associations and audit history")
	}
	for i := range archive.Projects {
		archive.Projects[i].ChecklistCompletionPolicy = ""
	}
	for i := range archive.Tasks {
		archive.Tasks[i].ParentTaskID = nil
		archive.Tasks[i].Checklist = nil
	}
}

// IDs may be deterministically remapped around destination collisions. Compare
// the complete ordered item payload while resolving task subtree identity.
func portableChecklistEqual(a, b []PortableChecklistItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		left, right := a[i], b[i]
		left.ID = ""
		right.ID = ""
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

// Checklist array order is canonicalized by the persisted position and ID,
// while preserving caller-owned task and item slices during validation.
func normalizePortableChecklistOrder(archive *PortableArchive) {
	archive.Tasks = append([]PortableTask(nil), archive.Tasks...)
	for i := range archive.Tasks {
		archive.Tasks[i].Checklist = append([]PortableChecklistItem(nil), archive.Tasks[i].Checklist...)
		sort.SliceStable(archive.Tasks[i].Checklist, func(a, b int) bool {
			left, right := archive.Tasks[i].Checklist[a], archive.Tasks[i].Checklist[b]
			if left.Position != right.Position {
				return left.Position < right.Position
			}
			return left.ID < right.ID
		})
	}
}

// Legacy archives have no project policy. Reusing an existing project retains
// its policy; new projects still default to warn with an omission warning.
func portableProjectPolicyMatches(existing, source PortableProject) bool {
	return source.ChecklistCompletionPolicy == "" || portableProjectPolicy(existing) == portableProjectPolicy(source)
}

// A legacy archive cannot identify a richer version of its own task subtree.
// Refuse the ambiguous collision rather than silently duplicating tasks while
// reporting a successful retry. A v4 export resolves the missing information.
func rejectLegacyRichTaskRetry(q portableSQL, sourceID, projectID string, report PortableImportReport) error {
	id := sourceID
	for attempt := -1; ; attempt++ {
		if attempt >= 0 {
			id = portableCandidateIDAt("task", sourceID, attempt)
		}
		task, exists, err := portableExistingTask(q, id)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if task.ProjectID == projectID && (task.ParentTaskID != nil || len(task.Checklist) > 0) {
			return portableConflictError(report, "legacy archive cannot retry a destination task with checklist or hierarchy; export version 4")
		}
	}
}
