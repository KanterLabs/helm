package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (s *Store) CreateTask(ctx context.Context, projectID string, input TaskInput, actorID string) (Task, error) {
	return s.createTask(ctx, projectID, input, actorID, nil)
}

// createTask runs afterInsert inside the creation transaction so dependent
// rows (such as ticket membership) commit atomically with the task.
func (s *Store) createTask(ctx context.Context, projectID string, input TaskInput, actorID string, afterInsert func(*sql.Tx, string) error) (Task, error) {
	validated, err := validateTaskInput(input, true)
	if err != nil {
		return Task{}, err
	}
	kind := defaultTaskKind
	if validated.Kind != nil {
		kind = *validated.Kind
	}
	if kind == bugKind {
		// ReporterID is intentionally the authenticated server actor rather
		// than client-controlled bug input. Validate it before opening the
		// mutation transaction so malformed bug creates fail cleanly.
		if strings.TrimSpace(actorID) == "" {
			return Task{}, invalid("a reporter actor is required for bugs", nil)
		}
		if _, err := s.GetActor(ctx, actorID); err != nil {
			return Task{}, invalid("reporter actor not found", nil)
		}
	}
	project, err := s.GetProject(ctx, projectID)
	if err != nil {
		return Task{}, err
	}
	var parent *hierarchyTask
	if validated.ParentTaskID != nil {
		resolved, parentErr := resolveHierarchyTask(ctx, s.DB, *validated.ParentTaskID)
		if parentErr != nil {
			return Task{}, parentErr
		}
		if resolved.ProjectID != project.ID {
			return Task{}, hierarchyInvalid(ErrHierarchyCrossProject, "parent and child must belong to the same project", map[string]any{
				"child_project_id":  project.ID,
				"parent_project_id": resolved.ProjectID,
			}, ErrInvalid)
		}
		parent = &resolved
	}
	columnID := ""
	if validated.ColumnID != nil {
		columnID = strings.TrimSpace(*validated.ColumnID)
	}
	var column Column
	if columnID == "" {
		column, err = s.backlogIntakeColumn(ctx, project.ID, kind)
	} else {
		column, err = s.GetColumn(ctx, columnID)
		if err == nil && column.ProjectID != project.ID {
			err = invalid("column belongs to another project", nil)
		}
	}
	if err != nil {
		return Task{}, err
	}
	releaseID := ""
	if validated.ReleaseID != nil {
		releaseID = *validated.ReleaseID
	}
	if column.ArchivedAt != nil {
		return Task{}, invalid("column is archived", nil)
	}
	if kind == bugKind && column.SemanticState == "completed" {
		return Task{}, invalid("bugs must be resolved before entering a completed column", nil)
	}
	priority := "normal"
	if validated.Priority != nil {
		priority = *validated.Priority
	}
	description := ""
	if validated.Description != nil {
		description = *validated.Description
	}
	position := 0.0
	if validated.Position != nil {
		position = *validated.Position
	} else {
		_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(position)+1, 0) FROM tasks WHERE column_id = ? AND deleted_at IS NULL`, column.ID).Scan(&position)
	}
	assignee := ""
	if validated.AssigneeSet && validated.Assignee != nil {
		assignee = strings.TrimSpace(*validated.Assignee)
	}
	if assignee != "" {
		if _, err := s.GetActor(ctx, assignee); err != nil {
			return Task{}, invalid("assignee actor not found", nil)
		}
	}
	dueAt := ""
	if validated.DueAtSet && validated.DueAt != nil {
		dueAt = *validated.DueAt
	}
	id, created := newID(), now()
	var number int
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_counters(project_id, next_number) VALUES (?, 2) ON CONFLICT(project_id) DO UPDATE SET next_number = project_counters.next_number + 1`, project.ID); err != nil {
			return err
		}
		// The counter write above acquires SQLite's writer lock before this
		// authoritative read. A column can be reclassified or archived after
		// preflight while this create waits; use the state that will actually
		// govern the inserted task and roll back the counter on rejection.
		destinationState, err := authoritativeColumnStateTx(ctx, tx, column.ID, project.ID)
		if err != nil {
			return err
		}
		if kind == bugKind && destinationState == "completed" {
			return invalid("bugs must be resolved before entering a completed column", nil)
		}
		var completedAt any
		if destinationState == "completed" {
			completedAt = created
		}
		if err := tx.QueryRowContext(ctx, `SELECT next_number - 1 FROM project_counters WHERE project_id = ?`, project.ID).Scan(&number); err != nil {
			return err
		}
		parentID := ""
		if parent != nil {
			parentID = parent.ID
		}
		if releaseID != "" {
			if err := validateTaskReleaseTx(ctx, tx, project.ID, releaseID, false); err != nil {
				return err
			}
		}
		if insertErr := txExecTaskCreate(ctx, tx, id, project.ID, number, column.ID, kind, *validated.Title, description, priority, position, assignee, dueAt, completedAt, created, parentID, releaseID); insertErr != nil {
			child := hierarchyTask{ID: id, ProjectID: project.ID, Number: number, Key: fmt.Sprintf("%s-%d", project.Key, number)}
			if parent != nil {
				return mapHierarchyMutationError(insertErr, child, *parent)
			}
			return insertErr
		}
		if validated.LabelsSet {
			if err := replaceTaskLabels(ctx, tx, id, project.ID, validated.Labels); err != nil {
				return err
			}
		}
		if kind == bugKind {
			if err := insertBugDetailsTx(ctx, tx, id, actorID, *validated.Bug); err != nil {
				return err
			}
		}
		if afterInsert != nil {
			if err := afterInsert(tx, id); err != nil {
				return err
			}
		}
		eventType := "task.created"
		if kind == bugKind {
			eventType = "bug.created"
		}
		payload := map[string]any{"number": number}
		if releaseID != "" {
			payload["release_id"] = releaseID
			if releaseName, releaseErr := releaseNameTx(ctx, tx, releaseID); releaseErr == nil {
				payload["release_name"] = releaseName
			}
		}
		if parent != nil {
			payload["parent_id"] = parent.ID
			payload["parent_key"] = parent.Key
		}
		if _, err := insertEvent(ctx, tx, eventType, actorID, project.ID, id, payload); err != nil {
			return err
		}
		if parent != nil {
			_, err := insertEvent(ctx, tx, "task.parent_linked", actorID, project.ID, id, map[string]any{
				"child_id":   id,
				"parent_id":  parent.ID,
				"parent_key": parent.Key,
				"version":    1,
			})
			return err
		}
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}
