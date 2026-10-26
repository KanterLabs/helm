package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type portableSQL interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func portableIDsClause(column string, ids []string) (string, []any) {
	if len(ids) == 0 {
		return "", nil
	}
	pl := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		pl[i] = "?"
		args[i] = id
	}
	return " AND " + column + " IN (" + strings.Join(pl, ",") + ")", args
}

// ExportPortable returns a stable, live-data snapshot. projectIDs nil means
// every project; callers should apply their authorization ceiling before
// calling this method. Deleted tasks are intentionally not resurrected by a
// portability export; SQLite backup/restore remains the recovery mechanism.
func (s *Store) ExportPortable(ctx context.Context, projectIDs []string) (PortableArchive, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return PortableArchive{}, err
	}
	archive, err := exportPortable(ctx, tx, projectIDs)
	if err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return PortableArchive{}, errors.Join(err, fmt.Errorf("rollback portable export: %w", rollbackErr))
		}
		return PortableArchive{}, err
	}
	if err := tx.Commit(); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return PortableArchive{}, errors.Join(err, fmt.Errorf("rollback portable export: %w", rollbackErr))
		}
		return PortableArchive{}, err
	}
	return archive, nil
}

func exportPortable(ctx context.Context, q portableSQL, projectIDs []string) (PortableArchive, error) {
	archive := PortableArchive{
		Format: PortableFormat, Version: PortableVersion, ExportedAt: now(),
		Source:   PortableSource{Product: "helm", API: "/api/v1"},
		Projects: []PortableProject{}, Columns: []PortableColumn{}, Releases: []PortableRelease{}, Tasks: []PortableTask{},
		Labels: []PortableLabel{}, Actors: []PortableActorReference{}, Comments: []PortableComment{}, AgentNotes: []PortableAgentNote{},
		Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
		Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{}},
	}
	where, args := portableIDsClause("p.id", projectIDs)
	rows, err := q.QueryContext(ctx, `SELECT p.id,p.key,p.slug,p.name,p.description,p.color,p.favorite,p.archived_at,p.created_at,p.updated_at FROM projects p WHERE 1=1`+where+` ORDER BY p.id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	projectSet := map[string]struct{}{}
	for rows.Next() {
		var p PortableProject
		var favorite int
		var archived sql.NullString
		if err := rows.Scan(&p.ID, &p.Key, &p.Slug, &p.Name, &p.Description, &p.Color, &favorite, &archived, &p.CreatedAt, &p.UpdatedAt); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		p.Favorite, p.ArchivedAt = boolValue(favorite), nullableString(archived)
		archive.Projects = append(archive.Projects, p)
		projectSet[p.ID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()
	if len(projectIDs) > 0 && len(archive.Projects) != len(projectIDs) {
		return PortableArchive{}, notFound("one or more projects not found")
	}
	if len(archive.Projects) == 0 {
		return archive, nil
	}
	projectIDs = make([]string, 0, len(archive.Projects))
	for _, project := range archive.Projects {
		projectIDs = append(projectIDs, project.ID)
	}
	where, args = portableIDsClause("c.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT c.id,c.project_id,c.name,c.semantic_state,c.position,c.created_at,c.updated_at FROM columns c WHERE 1=1`+where+` ORDER BY c.project_id,c.position,c.id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	columnSet := map[string]struct{}{}
	for rows.Next() {
		var c PortableColumn
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Name, &c.SemanticState, &c.Position, &c.CreatedAt, &c.UpdatedAt); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		archive.Columns = append(archive.Columns, c)
		columnSet[c.ID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	// Releases are project-scoped records. Export the complete release row
	// before tasks so task.release_id references are always represented in the
	// archive and actor references can include release completion attribution.
	where, args = portableIDsClause("r.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT r.id,r.project_id,r.name,r.description,r.target_date,r.released_at,r.released_by,r.version,r.created_at,r.updated_at FROM releases r WHERE 1=1`+where+` ORDER BY r.project_id,lower(r.name),r.id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	actorSet := map[string]struct{}{}
	for rows.Next() {
		var release PortableRelease
		var targetDate, releasedAt, releasedBy sql.NullString
		if err := rows.Scan(&release.ID, &release.ProjectID, &release.Name, &release.Description, &targetDate, &releasedAt, &releasedBy, &release.Version, &release.CreatedAt, &release.UpdatedAt); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		release.TargetDate, release.ReleasedAt, release.ReleasedBy = nullableString(targetDate), nullableString(releasedAt), nullableString(releasedBy)
		if release.ReleasedAt != nil {
			release.Status = "released"
		} else {
			release.Status = "planned"
		}
		archive.Releases = append(archive.Releases, release)
		if release.ReleasedBy != nil {
			actorSet[*release.ReleasedBy] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	where, args = portableIDsClause("t.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT t.id,t.number,t.project_id,t.kind,t.column_id,t.title,t.description,t.priority,t.position,t.assignee_id,t.claimed_by,t.claim_expires_at,t.due_at,t.version,t.completed_at,t.release_id,t.created_at,t.updated_at FROM tasks t WHERE t.deleted_at IS NULL`+where+` ORDER BY t.project_id,t.number,t.id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	taskSet := map[string]struct{}{}
	for rows.Next() {
		var task PortableTask
		var assignee, claimed, expiry, due, completed, releaseID sql.NullString
		if err := rows.Scan(&task.ID, &task.Number, &task.ProjectID, &task.Kind, &task.ColumnID, &task.Title, &task.Description, &task.Priority, &task.Position, &assignee, &claimed, &expiry, &due, &task.Version, &completed, &releaseID, &task.CreatedAt, &task.UpdatedAt); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		task.AssigneeID, task.ClaimedBy, task.ClaimExpiresAt, task.DueAt, task.CompletedAt, task.ReleaseID = nullableString(assignee), nullableString(claimed), nullableString(expiry), nullableString(due), nullableString(completed), nullableString(releaseID)
		archive.Tasks = append(archive.Tasks, task)
		taskSet[task.ID] = struct{}{}
		if task.AssigneeID != nil {
			actorSet[*task.AssigneeID] = struct{}{}
		}
		if task.ClaimedBy != nil {
			actorSet[*task.ClaimedBy] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	// Bug metadata is a separate one-to-one table. Keep it in the task record
	// so an archive round trip preserves the complete typed-task payload.
	where, args = portableIDsClause("t.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT bd.task_id,bd.reporter_id,bd.severity,bd.actual_behavior,bd.expected_behavior,bd.reproduction_steps,bd.environment,bd.affected_version,bd.resolution,bd.resolved_by,bd.resolved_at,bd.duplicate_of FROM bug_details bd JOIN tasks t ON t.id=bd.task_id WHERE t.deleted_at IS NULL`+where+` ORDER BY bd.task_id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	taskIndexes := make(map[string]int, len(archive.Tasks))
	for index, task := range archive.Tasks {
		taskIndexes[task.ID] = index
	}
	for rows.Next() {
		var taskID string
		var bug PortableBug
		var severity, resolution, resolvedBy, resolvedAt, duplicateOf sql.NullString
		if err := rows.Scan(&taskID, &bug.ReporterID, &severity, &bug.ActualBehavior, &bug.ExpectedBehavior, &bug.ReproductionSteps, &bug.Environment, &bug.AffectedVersion, &resolution, &resolvedBy, &resolvedAt, &duplicateOf); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		bug.Severity, bug.Resolution = nullableString(severity), nullableString(resolution)
		bug.ResolvedBy, bug.ResolvedAt, bug.DuplicateOf = nullableString(resolvedBy), nullableString(resolvedAt), nullableString(duplicateOf)
		if index, ok := taskIndexes[taskID]; ok {
			archive.Tasks[index].Bug = &bug
		}
		actorSet[bug.ReporterID] = struct{}{}
		if bug.ResolvedBy != nil {
			actorSet[*bug.ResolvedBy] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	where, args = portableIDsClause("l.project_id", projectIDs)
	rows, err = q.QueryContext(ctx, `SELECT l.id,l.project_id,l.name,l.color,l.created_at,l.updated_at FROM labels l WHERE 1=1`+where+` ORDER BY l.project_id,lower(l.name),l.id`, args...)
	if err != nil {
		return PortableArchive{}, err
	}
	labelSet := map[string]struct{}{}
	for rows.Next() {
		var label PortableLabel
		if err := rows.Scan(&label.ID, &label.ProjectID, &label.Name, &label.Color, &label.CreatedAt, &label.UpdatedAt); err != nil {
			rows.Close()
			return PortableArchive{}, err
		}
		archive.Labels = append(archive.Labels, label)
		labelSet[label.ID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PortableArchive{}, err
	}
	rows.Close()

	_ = columnSet // retained for clarity: column references are validated on import.
	return exportPortableRelated(ctx, q, archive, projectIDs, taskSet, labelSet, actorSet)
}
