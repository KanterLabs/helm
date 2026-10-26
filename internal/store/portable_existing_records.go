package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func portableExistingProject(q portableSQL, id string) (PortableProject, bool, error) {
	var project PortableProject
	var favorite int
	var archived sql.NullString
	err := q.QueryRowContext(context.Background(), `SELECT id,key,slug,name,description,color,favorite,archived_at,created_at,updated_at FROM projects WHERE id=?`, id).Scan(&project.ID, &project.Key, &project.Slug, &project.Name, &project.Description, &project.Color, &favorite, &archived, &project.CreatedAt, &project.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableProject{}, false, nil
	}
	if err != nil {
		return PortableProject{}, false, err
	}
	project.Favorite, project.ArchivedAt = boolValue(favorite), nullableString(archived)
	return project, true, nil
}

func portableExistingColumn(q portableSQL, id string) (PortableColumn, bool, error) {
	var column PortableColumn
	err := q.QueryRowContext(context.Background(), `SELECT id,project_id,name,semantic_state,position,created_at,updated_at FROM columns WHERE id=?`, id).Scan(&column.ID, &column.ProjectID, &column.Name, &column.SemanticState, &column.Position, &column.CreatedAt, &column.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableColumn{}, false, nil
	}
	if err != nil {
		return PortableColumn{}, false, err
	}
	return column, true, nil
}

func portableExistingRelease(q portableSQL, id string) (PortableRelease, bool, error) {
	var release PortableRelease
	var targetDate, releasedAt, releasedBy sql.NullString
	err := q.QueryRowContext(context.Background(), `SELECT id,project_id,name,description,target_date,released_at,released_by,version,created_at,updated_at FROM releases WHERE id=?`, id).Scan(&release.ID, &release.ProjectID, &release.Name, &release.Description, &targetDate, &releasedAt, &releasedBy, &release.Version, &release.CreatedAt, &release.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableRelease{}, false, nil
	}
	if err != nil {
		return PortableRelease{}, false, err
	}
	release.TargetDate, release.ReleasedAt, release.ReleasedBy = nullableString(targetDate), nullableString(releasedAt), nullableString(releasedBy)
	if release.ReleasedAt != nil {
		release.Status = "released"
	} else {
		release.Status = "planned"
	}
	return release, true, nil
}

func portableExistingLabel(q portableSQL, id string) (PortableLabel, bool, error) {
	var label PortableLabel
	err := q.QueryRowContext(context.Background(), `SELECT id,project_id,name,color,created_at,updated_at FROM labels WHERE id=?`, id).Scan(&label.ID, &label.ProjectID, &label.Name, &label.Color, &label.CreatedAt, &label.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableLabel{}, false, nil
	}
	if err != nil {
		return PortableLabel{}, false, err
	}
	return label, true, nil
}

func portableExistingTask(q portableSQL, id string) (PortableTask, bool, error) {
	var task PortableTask
	var assignee, claimed, expiry, due, completed, releaseID sql.NullString
	err := q.QueryRowContext(context.Background(), `SELECT id,number,project_id,kind,column_id,title,description,priority,position,assignee_id,claimed_by,claim_expires_at,due_at,version,completed_at,release_id,created_at,updated_at FROM tasks WHERE id=?`, id).Scan(&task.ID, &task.Number, &task.ProjectID, &task.Kind, &task.ColumnID, &task.Title, &task.Description, &task.Priority, &task.Position, &assignee, &claimed, &expiry, &due, &task.Version, &completed, &releaseID, &task.CreatedAt, &task.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableTask{}, false, nil
	}
	if err != nil {
		return PortableTask{}, false, err
	}
	task.AssigneeID, task.ClaimedBy, task.ClaimExpiresAt, task.DueAt, task.CompletedAt, task.ReleaseID = nullableString(assignee), nullableString(claimed), nullableString(expiry), nullableString(due), nullableString(completed), nullableString(releaseID)
	var bug Bug
	bug, bugErr := bugFromRow(q.QueryRowContext(context.Background(), `SELECT reporter_id, severity, actual_behavior, expected_behavior, reproduction_steps, environment, affected_version, resolution, resolved_by, resolved_at, duplicate_of FROM bug_details WHERE task_id=?`, id))
	if bugErr == nil {
		task.Bug = &PortableBug{ReporterID: bug.ReporterID, Severity: bug.Severity, ActualBehavior: bug.ActualBehavior, ExpectedBehavior: bug.ExpectedBehavior, ReproductionSteps: bug.ReproductionSteps, Environment: bug.Environment, AffectedVersion: bug.AffectedVersion, Resolution: bug.Resolution, ResolvedBy: bug.ResolvedBy, ResolvedAt: bug.ResolvedAt, DuplicateOf: bug.DuplicateOf}
	} else if !errors.Is(bugErr, sql.ErrNoRows) {
		return PortableTask{}, false, bugErr
	}
	return task, true, nil
}

func portableExistingComment(q portableSQL, id string) (PortableComment, bool, error) {
	var comment PortableComment
	err := q.QueryRowContext(context.Background(), `SELECT id,task_id,actor_id,body,created_at,updated_at FROM comments WHERE id=?`, id).Scan(&comment.ID, &comment.TaskID, &comment.ActorID, &comment.Body, &comment.CreatedAt, &comment.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableComment{}, false, nil
	}
	if err != nil {
		return PortableComment{}, false, err
	}
	return comment, true, nil
}

func portableExistingAgentNote(q portableSQL, id string) (PortableAgentNote, bool, error) {
	var note PortableAgentNote
	var evidence string
	var resolved sql.NullString
	err := q.QueryRowContext(context.Background(), `SELECT id,task_id,actor_id,category,body,evidence_json,version,created_at,updated_at,resolved_at FROM agent_notes WHERE id=?`, id).Scan(&note.ID, &note.TaskID, &note.ActorID, &note.Category, &note.Body, &evidence, &note.Version, &note.CreatedAt, &note.UpdatedAt, &resolved)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableAgentNote{}, false, nil
	}
	if err != nil {
		return PortableAgentNote{}, false, err
	}
	if err := json.Unmarshal([]byte(evidence), &note.Evidence); err != nil {
		return PortableAgentNote{}, false, err
	}
	note.ResolvedAt = nullableString(resolved)
	return note, true, nil
}

func portableIDSet(q portableSQL, table string) (map[string]struct{}, error) {
	// table is selected only from fixed internal call sites below; never pass
	// user input here.
	rows, err := q.QueryContext(context.Background(), `SELECT id FROM `+table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = struct{}{}
	}
	return result, rows.Err()
}

func portableProjectFieldsEqual(a PortableProject, b PortableProject) bool {
	return a.Key == b.Key && a.Slug == b.Slug && a.Name == b.Name && a.Description == b.Description && a.Color == b.Color && a.Favorite == b.Favorite && stringPointerValue(a.ArchivedAt) == stringPointerValue(b.ArchivedAt) && a.CreatedAt == b.CreatedAt && a.UpdatedAt == b.UpdatedAt
}

func portableProjectMetadataEqual(a PortableProject, b PortableProject) bool {
	return a.Name == b.Name && a.Description == b.Description && a.Color == b.Color && a.Favorite == b.Favorite && stringPointerValue(a.ArchivedAt) == stringPointerValue(b.ArchivedAt) && a.CreatedAt == b.CreatedAt && a.UpdatedAt == b.UpdatedAt
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func portableColumnFieldsEqual(a PortableColumn, b PortableColumn) bool {
	return a.ProjectID == b.ProjectID && a.Name == b.Name && a.SemanticState == b.SemanticState && a.Position == b.Position && a.CreatedAt == b.CreatedAt && a.UpdatedAt == b.UpdatedAt
}

func portableColumnFieldsEqualIgnoringPosition(a PortableColumn, b PortableColumn) bool {
	a.Position, b.Position = 0, 0
	return portableColumnFieldsEqual(a, b)
}

func portableReleaseStatus(release PortableRelease) string {
	if release.ReleasedAt != nil {
		return "released"
	}
	return "planned"
}

func portableReleaseFieldsEqual(a, b PortableRelease) bool {
	return a.ProjectID == b.ProjectID && a.Name == b.Name && a.Description == b.Description && stringPointerValue(a.TargetDate) == stringPointerValue(b.TargetDate) && stringPointerValue(a.ReleasedAt) == stringPointerValue(b.ReleasedAt) && stringPointerValue(a.ReleasedBy) == stringPointerValue(b.ReleasedBy) && a.Version == b.Version && a.CreatedAt == b.CreatedAt && a.UpdatedAt == b.UpdatedAt
}

func portableLabelFieldsEqual(a PortableLabel, b PortableLabel) bool {
	return a.ProjectID == b.ProjectID && strings.EqualFold(a.Name, b.Name) && a.Color == b.Color && a.CreatedAt == b.CreatedAt && a.UpdatedAt == b.UpdatedAt
}

func portableTaskFieldsEqual(a, b PortableTask) bool {
	return a.Number == b.Number && a.ProjectID == b.ProjectID && a.Kind == b.Kind && a.ColumnID == b.ColumnID && a.Title == b.Title && a.Description == b.Description && a.Priority == b.Priority && a.Position == b.Position && stringPointerValue(a.AssigneeID) == stringPointerValue(b.AssigneeID) && stringPointerValue(a.ClaimedBy) == stringPointerValue(b.ClaimedBy) && stringPointerValue(a.ClaimExpiresAt) == stringPointerValue(b.ClaimExpiresAt) && stringPointerValue(a.DueAt) == stringPointerValue(b.DueAt) && a.Version == b.Version && stringPointerValue(a.CompletedAt) == stringPointerValue(b.CompletedAt) && stringPointerValue(a.ReleaseID) == stringPointerValue(b.ReleaseID) && a.CreatedAt == b.CreatedAt && a.UpdatedAt == b.UpdatedAt && portableBugFieldsEqual(a.Bug, b.Bug)
}

func portableTaskFieldsEqualIgnoringNumber(a, b PortableTask) bool {
	a.Number, b.Number = 0, 0
	return portableTaskFieldsEqual(a, b)
}

func portableBugFieldsEqual(a, b *PortableBug) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ReporterID == b.ReporterID && stringPointerValue(a.Severity) == stringPointerValue(b.Severity) && a.ActualBehavior == b.ActualBehavior && a.ExpectedBehavior == b.ExpectedBehavior && a.ReproductionSteps == b.ReproductionSteps && a.Environment == b.Environment && a.AffectedVersion == b.AffectedVersion && stringPointerValue(a.Resolution) == stringPointerValue(b.Resolution) && stringPointerValue(a.ResolvedBy) == stringPointerValue(b.ResolvedBy) && stringPointerValue(a.ResolvedAt) == stringPointerValue(b.ResolvedAt) && stringPointerValue(a.DuplicateOf) == stringPointerValue(b.DuplicateOf)
}

func portableCommentFieldsEqual(a, b PortableComment) bool {
	return a.TaskID == b.TaskID && a.ActorID == b.ActorID && a.Body == b.Body && a.CreatedAt == b.CreatedAt && a.UpdatedAt == b.UpdatedAt
}

func portableAgentNoteFieldsEqual(a, b PortableAgentNote) bool {
	if a.TaskID != b.TaskID || a.ActorID != b.ActorID || a.Category != b.Category || a.Body != b.Body || a.Version != b.Version || a.CreatedAt != b.CreatedAt || a.UpdatedAt != b.UpdatedAt || stringPointerValue(a.ResolvedAt) != stringPointerValue(b.ResolvedAt) || len(a.Evidence) != len(b.Evidence) {
		return false
	}
	for index := range a.Evidence {
		if a.Evidence[index] != b.Evidence[index] {
			return false
		}
	}
	return true
}
