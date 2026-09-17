package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const (
	maxReleaseName        = 200
	maxReleaseDescription = 10000
)

const releaseColumns = `id, project_id, name, description, target_date,
	released_at, released_by, version, created_at, updated_at`

func releaseStatus(releasedAt *string) string {
	if releasedAt != nil {
		return "released"
	}
	return "planned"
}

func releaseFromRow(scanner interface{ Scan(...any) error }) (Release, error) {
	var release Release
	var targetDate, releasedAt, releasedBy sql.NullString
	if err := scanner.Scan(&release.ID, &release.ProjectID, &release.Name, &release.Description, &targetDate, &releasedAt, &releasedBy, &release.Version, &release.CreatedAt, &release.UpdatedAt); err != nil {
		return Release{}, err
	}
	release.TargetDate = nullableString(targetDate)
	release.ReleasedAt = nullableString(releasedAt)
	release.ReleasedBy = nullableString(releasedBy)
	release.Status = releaseStatus(release.ReleasedAt)
	return release, nil
}

func releaseReferenceFromRow(scanner interface{ Scan(...any) error }) (ReleaseReference, error) {
	var reference ReleaseReference
	var releasedAt, targetDate sql.NullString
	if err := scanner.Scan(&reference.ID, &reference.Name, &releasedAt, &targetDate); err != nil {
		return ReleaseReference{}, err
	}
	reference.Status = "planned"
	if releasedAt.Valid {
		reference.Status = "released"
	}
	reference.TargetDate = nullableString(targetDate)
	return reference, nil
}

func releaseError(kind, fallback error, message string, details any) error {
	if fallback == nil {
		return &Error{Kind: kind, Message: message, Details: details}
	}
	return &Error{Kind: errors.Join(kind, fallback), Message: message, Details: details}
}

func validateReleaseTargetDate(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil, invalid("target_date must be an ISO YYYY-MM-DD date", nil)
	}
	parsed, err := time.Parse("2006-01-02", normalized)
	if err != nil || parsed.Format("2006-01-02") != normalized {
		return nil, invalid("target_date must be an ISO YYYY-MM-DD date", nil)
	}
	return &normalized, nil
}

func validateReleaseInput(input ReleaseInput, creating bool) (ReleaseInput, error) {
	if creating && input.Name == nil {
		return ReleaseInput{}, invalid("name is required", nil)
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len(name) > maxReleaseName {
			return ReleaseInput{}, invalid("name must be between 1 and 200 characters", nil)
		}
		if reservedReleaseName(name) {
			return ReleaseInput{}, invalid("name must not be unassigned or none", nil)
		}
		input.Name = &name
	}
	if input.Description != nil {
		description := strings.TrimSpace(*input.Description)
		if len(description) > maxReleaseDescription {
			return ReleaseInput{}, invalid("description is too long", nil)
		}
		input.Description = &description
	}
	date, err := validateReleaseTargetDate(input.TargetDate)
	if err != nil {
		return ReleaseInput{}, err
	}
	input.TargetDate = date
	return input, nil
}

func reservedReleaseName(name string) bool {
	name = strings.TrimSpace(name)
	return strings.EqualFold(name, "unassigned") || strings.EqualFold(name, "none")
}

func releaseNameTx(ctx context.Context, q dependencySQL, releaseID string) (string, error) {
	var name string
	if err := q.QueryRowContext(ctx, `SELECT name FROM releases WHERE id=?`, releaseID).Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}

// validateTaskReleaseTx is called inside the task writer transaction. It is
// intentionally authoritative even when callers already performed a read:
// another writer may complete a release while this task waits for SQLite's
// writer lock.
func validateTaskReleaseTx(ctx context.Context, q dependencySQL, projectID, releaseID string, allowReleased bool) error {
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return nil
	}
	var releaseProject string
	var releasedAt sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT project_id, released_at FROM releases WHERE id=?`, releaseID).Scan(&releaseProject, &releasedAt); errors.Is(err, sql.ErrNoRows) {
		return releaseError(ErrReleaseNotFound, ErrNotFound, "release not found", map[string]any{"release_id": releaseID, "project_id": projectID})
	} else if err != nil {
		return err
	}
	if releaseProject != projectID {
		return releaseError(ErrReleaseCrossProject, ErrInvalid, "release belongs to another project", map[string]any{"release_id": releaseID, "project_id": projectID, "release_project_id": releaseProject})
	}
	if releasedAt.Valid && !allowReleased {
		return releaseError(ErrReleaseFrozen, ErrConflict, "released releases cannot accept new task members", map[string]any{"release_id": releaseID})
	}
	return nil
}

func mapReleaseMutationError(_ context.Context, _ dependencySQL, err error, projectID, taskID string) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "release_cross_project_or_not_live"):
		return releaseError(ErrReleaseCrossProject, ErrInvalid, "release must belong to the same project and remain planned", map[string]any{"project_id": projectID, "task_id": taskID})
	case strings.Contains(lower, "release_frozen"):
		return releaseError(ErrReleaseFrozen, ErrConflict, "released release membership is frozen", map[string]any{"project_id": projectID, "task_id": taskID})
	case strings.Contains(lower, "release_has_tasks"):
		return releaseError(ErrReleaseHasTasks, ErrConflict, "release has task members", map[string]any{"project_id": projectID})
	case strings.Contains(lower, "release_incomplete"):
		return releaseError(ErrReleaseIncomplete, ErrConflict, "release has incomplete required work", map[string]any{"project_id": projectID})
	case strings.Contains(lower, "release_dependency_conflict"):
		return releaseError(ErrReleaseDependencyConflict, ErrConflict, "release has a dependency conflict", map[string]any{"project_id": projectID})
	case strings.Contains(lower, "unique constraint failed") && strings.Contains(lower, "releases"):
		return releaseError(ErrReleaseNameExists, ErrAlreadyExists, "release name already exists in this project", map[string]any{"project_id": projectID})
	default:
		return err
	}
}

// populateTaskReleaseReference enriches one task read. Collections use the
// batched sibling below so adding release chips does not create one query per
// board card.
func (s *Store) populateTaskReleaseReference(ctx context.Context, task *Task) error {
	task.Release = nil
	if task.ReleaseID == nil || strings.TrimSpace(*task.ReleaseID) == "" {
		return nil
	}
	reference, err := releaseReferenceFromRow(s.DB.QueryRowContext(ctx, `SELECT id, name, released_at, target_date FROM releases WHERE id=?`, *task.ReleaseID))
	if errors.Is(err, sql.ErrNoRows) {
		// Foreign-key enforcement prevents this for normal writes. Treat a
		// malformed retained row as an unexpanded reference rather than leaking
		// an internal SQL no-rows error through an otherwise valid task read.
		return nil
	}
	if err != nil {
		return err
	}
	task.Release = &reference
	return nil
}

func (s *Store) populateTaskReleaseReferences(ctx context.Context, tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]string, 0, len(tasks))
	seen := make(map[string]struct{}, len(tasks))
	for index := range tasks {
		tasks[index].Release = nil
		if tasks[index].ReleaseID == nil || strings.TrimSpace(*tasks[index].ReleaseID) == "" {
			continue
		}
		id := strings.TrimSpace(*tasks[index].ReleaseID)
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for index, id := range ids {
		placeholders[index], args[index] = "?", id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, released_at, target_date FROM releases WHERE id IN (`+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return err
	}
	references := make(map[string]ReleaseReference, len(ids))
	for rows.Next() {
		reference, scanErr := releaseReferenceFromRow(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		references[reference.ID] = reference
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for index := range tasks {
		if tasks[index].ReleaseID == nil {
			continue
		}
		if reference, exists := references[strings.TrimSpace(*tasks[index].ReleaseID)]; exists {
			reference := reference
			tasks[index].Release = &reference
		}
	}
	return nil
}

func (s *Store) GetRelease(ctx context.Context, id string) (Release, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Release{}, releaseError(ErrReleaseNotFound, ErrNotFound, "release not found", nil)
	}
	release, err := releaseFromRow(s.DB.QueryRowContext(ctx, `SELECT `+releaseColumns+` FROM releases WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, releaseError(ErrReleaseNotFound, ErrNotFound, "release not found", map[string]any{"release_id": id})
	}
	if err != nil {
		return Release{}, err
	}
	if err := s.populateReleaseSummary(ctx, &release); err != nil {
		return Release{}, err
	}
	return release, nil
}

// ResolveReleaseReference accepts an opaque release ID or a case-insensitive
// name within projectID. A project ID is required so identical names in two
// projects cannot be confused by a cross-project caller.
func (s *Store) ResolveReleaseReference(ctx context.Context, projectID, reference string) (Release, error) {
	projectID, reference = strings.TrimSpace(projectID), strings.TrimSpace(reference)
	if projectID == "" || reference == "" {
		return Release{}, releaseError(ErrReleaseNotFound, ErrNotFound, "release not found", map[string]any{"reference": reference})
	}
	release, err := releaseFromRow(s.DB.QueryRowContext(ctx, `SELECT `+releaseColumns+` FROM releases WHERE project_id=? AND (id=? OR lower(name)=lower(?)) LIMIT 1`, projectID, reference, reference))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, releaseError(ErrReleaseNotFound, ErrNotFound, "release not found", map[string]any{"reference": reference, "project_id": projectID})
	}
	if err != nil {
		return Release{}, err
	}
	if err := s.populateReleaseSummary(ctx, &release); err != nil {
		return Release{}, err
	}
	return release, nil
}

func (s *Store) GetReleaseByName(ctx context.Context, projectID, name string) (Release, error) {
	return s.ResolveReleaseReference(ctx, projectID, name)
}

func (s *Store) ListReleases(ctx context.Context, projectID string, filter ReleaseFilter) ([]Release, bool, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, false, invalid("project_id is required", nil)
	}
	if filter.Status != "" && filter.Status != "planned" && filter.Status != "released" {
		return nil, false, invalid("status must be planned or released", nil)
	}
	from, err := validateReleaseTargetDate(filter.TargetFrom)
	if err != nil {
		return nil, false, err
	}
	to, err := validateReleaseTargetDate(filter.TargetTo)
	if err != nil {
		return nil, false, err
	}
	if from != nil && to != nil && *from > *to {
		return nil, false, invalid("target_from must not be after target_to", nil)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := filter.Cursor
	if offset < 0 {
		offset = 0
	}
	query := `SELECT ` + releaseColumns + ` FROM releases WHERE project_id=?`
	args := []any{projectID}
	if filter.Status == "planned" {
		query += ` AND released_at IS NULL`
	} else if filter.Status == "released" {
		query += ` AND released_at IS NOT NULL`
	}
	if from != nil {
		query += ` AND target_date >= ?`
		args = append(args, *from)
	}
	if to != nil {
		query += ` AND target_date <= ?`
		args = append(args, *to)
	}
	query += ` ORDER BY released_at IS NOT NULL, target_date IS NULL, target_date, created_at, id LIMIT ? OFFSET ?`
	args = append(args, limit+1, offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	result := make([]Release, 0, limit)
	for rows.Next() {
		release, scanErr := releaseFromRow(rows)
		if scanErr != nil {
			rows.Close()
			return nil, false, scanErr
		}
		result = append(result, release)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, err
	}
	rows.Close()
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	for index := range result {
		if err := s.populateReleaseSummary(ctx, &result[index]); err != nil {
			return nil, false, err
		}
	}
	return result, hasMore, nil
}

func (s *Store) ListProjectReleases(ctx context.Context, projectID string, filter ReleaseFilter) ([]Release, bool, error) {
	return s.ListReleases(ctx, projectID, filter)
}

func (s *Store) CreateRelease(ctx context.Context, projectID string, input ReleaseInput, actorID string) (Release, error) {
	validated, err := validateReleaseInput(input, true)
	if err != nil {
		return Release{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return Release{}, invalid("project_id is required", nil)
	}
	project, err := s.GetProject(ctx, projectID)
	if err != nil {
		return Release{}, err
	}
	projectID = project.ID
	description := ""
	if validated.Description != nil {
		description = *validated.Description
	}
	targetDate := ""
	if validated.TargetDate != nil {
		targetDate = *validated.TargetDate
	}
	id, created := newID(), now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO releases(id, project_id, name, description, target_date, version, created_at, updated_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), 1, ?, ?)`, id, projectID, *validated.Name, description, targetDate, created, created); err != nil {
			return mapReleaseMutationError(ctx, tx, err, projectID, "")
		}
		var targetDateValue any
		if validated.TargetDate != nil {
			targetDateValue = *validated.TargetDate
		}
		_, err := insertEvent(ctx, tx, "release.created", actorID, projectID, "", map[string]any{
			"release_id":  id,
			"name":        *validated.Name,
			"target_date": targetDateValue,
			"status":      "planned",
			"version":     1,
		})
		return err
	})
	if err != nil {
		return Release{}, err
	}
	return s.GetRelease(ctx, id)
}

func (s *Store) UpdateRelease(ctx context.Context, id string, input ReleaseInput, expected int64, actorID string) (Release, error) {
	validated, err := validateReleaseInput(input, false)
	if err != nil {
		return Release{}, err
	}
	if expected <= 0 {
		return Release{}, ErrPrecondition
	}
	current, err := s.GetRelease(ctx, id)
	if err != nil {
		return Release{}, err
	}
	if current.ReleasedAt != nil {
		return Release{}, releaseError(ErrReleaseFrozen, ErrConflict, "released release is frozen; reopen it first", map[string]any{"release_id": current.ID})
	}
	name, description := current.Name, current.Description
	targetDate := nullableStringValue(current.TargetDate)
	if validated.Name != nil {
		name = *validated.Name
	}
	if validated.DescriptionSet {
		description = ""
		if validated.Description != nil {
			description = *validated.Description
		}
	} else if validated.Description != nil {
		description = *validated.Description
	}
	if validated.TargetDateSet || validated.TargetDate != nil {
		targetDate = ""
		if validated.TargetDate != nil {
			targetDate = *validated.TargetDate
		}
	}
	updated := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		result, updateErr := tx.ExecContext(ctx, `UPDATE releases SET name=?, description=?, target_date=NULLIF(?, ''), version=version+1, updated_at=? WHERE id=? AND version=? AND released_at IS NULL`, name, description, targetDate, updated, current.ID, expected)
		if updateErr != nil {
			return mapReleaseMutationError(ctx, tx, updateErr, current.ProjectID, "")
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if changed != 1 {
			var version int64
			var releasedAt sql.NullString
			if readErr := tx.QueryRowContext(ctx, `SELECT version, released_at FROM releases WHERE id=?`, current.ID).Scan(&version, &releasedAt); errors.Is(readErr, sql.ErrNoRows) {
				return releaseError(ErrReleaseNotFound, ErrNotFound, "release not found", map[string]any{"release_id": current.ID})
			} else if readErr != nil {
				return readErr
			} else if releasedAt.Valid {
				return releaseError(ErrReleaseFrozen, ErrConflict, "released release is frozen; reopen it first", map[string]any{"release_id": current.ID})
			}
			return conflict("release has changed", map[string]any{"current_version": version, "expected_version": expected})
		}
		var targetDateValue any
		if targetDate != "" {
			targetDateValue = targetDate
		}
		_, eventErr := insertEvent(ctx, tx, "release.updated", actorID, current.ProjectID, "", map[string]any{
			"release_id":  id,
			"name":        name,
			"target_date": targetDateValue,
			"status":      "planned",
			"version":     expected + 1,
		})
		return eventErr
	})
	if err != nil {
		return Release{}, err
	}
	return s.GetRelease(ctx, current.ID)
}

func (s *Store) DeleteRelease(ctx context.Context, id string, expected int64, actorID string) error {
	if expected <= 0 {
		return ErrPrecondition
	}
	current, err := s.GetRelease(ctx, id)
	if err != nil {
		return err
	}
	if current.ReleasedAt != nil {
		return releaseError(ErrReleaseFrozen, ErrConflict, "released release is frozen; reopen it first", map[string]any{"release_id": current.ID})
	}
	err = s.withImmediateTx(ctx, func(tx dependencySQL) error {
		var releasedAt sql.NullString
		if readErr := tx.QueryRowContext(ctx, `SELECT released_at FROM releases WHERE id=?`, current.ID).Scan(&releasedAt); errors.Is(readErr, sql.ErrNoRows) {
			return releaseError(ErrReleaseNotFound, ErrNotFound, "release not found", map[string]any{"release_id": current.ID})
		} else if readErr != nil {
			return readErr
		}
		if releasedAt.Valid {
			return releaseError(ErrReleaseFrozen, ErrConflict, "released release is frozen; reopen it first", map[string]any{"release_id": current.ID})
		}
		// Check the foreign-key membership boundary inside the same immediate
		// writer transaction as the delete. This keeps the public lifecycle
		// error stable even when a task was attached while the caller was
		// preparing the request, and avoids exposing a driver-specific FK error.
		var taskCount int
		if countErr := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks WHERE release_id=?`, current.ID).Scan(&taskCount); countErr != nil {
			return countErr
		}
		if taskCount > 0 {
			return releaseError(ErrReleaseHasTasks, ErrConflict, "release has task members", map[string]any{"release_id": current.ID, "task_count": taskCount})
		}
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM releases WHERE id=? AND version=? AND released_at IS NULL`, current.ID, expected)
		if deleteErr != nil {
			return mapReleaseMutationError(ctx, tx, deleteErr, current.ProjectID, "")
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if changed != 1 {
			var version int64
			var releasedAt sql.NullString
			if readErr := tx.QueryRowContext(ctx, `SELECT version, released_at FROM releases WHERE id=?`, current.ID).Scan(&version, &releasedAt); errors.Is(readErr, sql.ErrNoRows) {
				return releaseError(ErrReleaseNotFound, ErrNotFound, "release not found", map[string]any{"release_id": current.ID})
			} else if readErr != nil {
				return readErr
			} else if releasedAt.Valid {
				return releaseError(ErrReleaseFrozen, ErrConflict, "released release is frozen; reopen it first", map[string]any{"release_id": current.ID})
			}
			return conflict("release has changed", map[string]any{"current_version": version, "expected_version": expected})
		}
		_, eventErr := insertEvent(ctx, tx, "release.deleted", actorID, current.ProjectID, "", map[string]any{
			"release_id": id,
			"name":       current.Name,
			"status":     "planned",
		})
		return eventErr
	})
	return err
}

func (s *Store) CompleteRelease(ctx context.Context, id string, expected int64, actorID string) (Release, error) {
	if expected <= 0 {
		return Release{}, ErrPrecondition
	}
	current, err := s.GetRelease(ctx, id)
	if err != nil {
		return Release{}, err
	}
	if current.ReleasedAt != nil {
		return Release{}, releaseError(ErrReleaseAlreadyCompleted, ErrConflict, "release is already released", map[string]any{"release_id": current.ID})
	}
	releasedAt := now()
	err = s.withImmediateTx(ctx, func(tx dependencySQL) error {
		summary, summaryErr := releaseSummaryTx(ctx, tx, current.ID)
		if summaryErr != nil {
			return summaryErr
		}
		if summary.CrossReleaseConflictCount > 0 {
			return releaseError(ErrReleaseDependencyConflict, ErrConflict, "release has a cross-release dependency conflict", map[string]any{"release_id": current.ID, "summary": summary})
		}
		if summary.TaskCount == 0 || summary.RequiredCompletedCount != summary.RequiredTaskCount {
			return releaseError(ErrReleaseIncomplete, ErrConflict, "release has incomplete required work", map[string]any{"release_id": current.ID, "summary": summary})
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE releases SET released_at=?, released_by=NULLIF(?, ''), version=version+1, updated_at=? WHERE id=? AND version=? AND released_at IS NULL`, releasedAt, actorID, releasedAt, current.ID, expected)
		if updateErr != nil {
			return mapReleaseMutationError(ctx, tx, updateErr, current.ProjectID, "")
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if changed != 1 {
			var version int64
			var released sql.NullString
			if readErr := tx.QueryRowContext(ctx, `SELECT version, released_at FROM releases WHERE id=?`, current.ID).Scan(&version, &released); errors.Is(readErr, sql.ErrNoRows) {
				return releaseError(ErrReleaseNotFound, ErrNotFound, "release not found", map[string]any{"release_id": current.ID})
			} else if readErr != nil {
				return readErr
			} else if released.Valid {
				return releaseError(ErrReleaseAlreadyCompleted, ErrConflict, "release is already released", map[string]any{"release_id": current.ID})
			}
			return conflict("release has changed", map[string]any{"current_version": version, "expected_version": expected})
		}
		_, eventErr := insertEvent(ctx, tx, "release.completed", actorID, current.ProjectID, "", map[string]any{
			"release_id":  current.ID,
			"name":        current.Name,
			"status":      "released",
			"released_at": releasedAt,
			"released_by": actorID,
			"version":     expected + 1,
		})
		return eventErr
	})
	if err != nil {
		return Release{}, err
	}
	return s.GetRelease(ctx, current.ID)
}

// ReopenRelease reopens a released release after a required human-readable
// reason. Membership and task lifecycle guards then become writable again.
func (s *Store) ReopenRelease(ctx context.Context, id, reason string, expected int64, actorID string) (Release, error) {
	if expected <= 0 {
		return Release{}, ErrPrecondition
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Release{}, invalid("reopen reason is required", nil)
	}
	if len(reason) > maxReleaseDescription {
		return Release{}, invalid("reopen reason is too long", nil)
	}
	current, err := s.GetRelease(ctx, id)
	if err != nil {
		return Release{}, err
	}
	if current.ReleasedAt == nil {
		return Release{}, releaseError(ErrReleaseNotCompleted, ErrConflict, "release is not released", map[string]any{"release_id": current.ID})
	}
	updated := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		result, updateErr := tx.ExecContext(ctx, `UPDATE releases SET released_at=NULL, released_by=NULL, version=version+1, updated_at=? WHERE id=? AND version=? AND released_at IS NOT NULL`, updated, current.ID, expected)
		if updateErr != nil {
			return mapReleaseMutationError(ctx, tx, updateErr, current.ProjectID, "")
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if changed != 1 {
			var version int64
			var released sql.NullString
			if readErr := tx.QueryRowContext(ctx, `SELECT version, released_at FROM releases WHERE id=?`, current.ID).Scan(&version, &released); errors.Is(readErr, sql.ErrNoRows) {
				return releaseError(ErrReleaseNotFound, ErrNotFound, "release not found", map[string]any{"release_id": current.ID})
			} else if readErr != nil {
				return readErr
			} else if !released.Valid {
				return releaseError(ErrReleaseNotCompleted, ErrConflict, "release is not released", map[string]any{"release_id": current.ID})
			}
			return conflict("release has changed", map[string]any{"current_version": version, "expected_version": expected})
		}
		_, eventErr := insertEvent(ctx, tx, "release.reopened", actorID, current.ProjectID, "", map[string]any{
			"release_id": current.ID,
			"name":       current.Name,
			"status":     "planned",
			"reason":     reason,
			"version":    expected + 1,
		})
		return eventErr
	})
	if err != nil {
		return Release{}, err
	}
	return s.GetRelease(ctx, current.ID)
}

func (s *Store) ReopenReleaseWithReason(ctx context.Context, id, reason string, expected int64, actorID string) (Release, error) {
	return s.ReopenRelease(ctx, id, reason, expected, actorID)
}

func releaseSummaryTx(ctx context.Context, q dependencySQL, releaseID string) (ReleaseSummary, error) {
	var summary ReleaseSummary
	var warning int
	if err := q.QueryRowContext(ctx, `SELECT
		COUNT(1),
		COALESCE(SUM(CASE WHEN c.semantic_state='completed' AND t.completed_at IS NOT NULL THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN c.semantic_state='blocked' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN t.claimed_by IS NOT NULL AND t.claim_expires_at IS NOT NULL AND julianday(t.claim_expires_at) > julianday('now') THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN c.semantic_state='completed' AND t.completed_at IS NOT NULL AND EXISTS (SELECT 1 FROM task_checklist_items item WHERE item.task_id=t.id AND item.completed=0) AND COALESCE((SELECT checklist_completion_policy FROM projects WHERE id=t.project_id), 'warn')='warn' THEN 1 ELSE 0 END), 0)
		FROM tasks t JOIN columns c ON c.id=t.column_id
		WHERE t.release_id=? AND t.deleted_at IS NULL`, releaseID).Scan(&summary.TaskCount, &summary.CompletedCount, &summary.BlockedCount, &summary.ClaimedCount, &warning); err != nil {
		return ReleaseSummary{}, err
	}
	summary.ChecklistWarningCount = warning

	closure := `WITH RECURSIVE required(task_id) AS (
		SELECT id FROM tasks WHERE release_id=? AND deleted_at IS NULL
		UNION
		SELECT dependency.prerequisite_task_id
		FROM task_dependencies dependency
		JOIN required current ON current.task_id=dependency.task_id
		JOIN tasks dependent ON dependent.id=dependency.task_id AND dependent.deleted_at IS NULL
	)
	SELECT COUNT(1), COALESCE(SUM(CASE WHEN required_task.completed_at IS NOT NULL AND required_column.semantic_state='completed' THEN 1 ELSE 0 END), 0)
	FROM required
	JOIN tasks required_task ON required_task.id=required.task_id AND required_task.deleted_at IS NULL
	LEFT JOIN columns required_column ON required_column.id=required_task.column_id AND required_column.project_id=required_task.project_id`
	if err := q.QueryRowContext(ctx, closure, releaseID).Scan(&summary.RequiredTaskCount, &summary.RequiredCompletedCount); err != nil {
		return ReleaseSummary{}, err
	}
	conflicts := `WITH RECURSIVE required(task_id) AS (
		SELECT id FROM tasks WHERE release_id=? AND deleted_at IS NULL
		UNION
		SELECT dependency.prerequisite_task_id
		FROM task_dependencies dependency
		JOIN required current ON current.task_id=dependency.task_id
		JOIN tasks dependent ON dependent.id=dependency.task_id AND dependent.deleted_at IS NULL
	)
	SELECT COUNT(1)
	FROM required
	JOIN tasks required_task ON required_task.id=required.task_id AND required_task.deleted_at IS NULL
	JOIN releases other_release ON other_release.id=required_task.release_id
	JOIN columns required_column ON required_column.id=required_task.column_id
	WHERE required_task.release_id IS NOT NULL
	  AND required_task.release_id <> ?
	  AND other_release.released_at IS NULL
	  AND (required_task.completed_at IS NULL OR required_column.semantic_state <> 'completed')`
	if err := q.QueryRowContext(ctx, conflicts, releaseID, releaseID).Scan(&summary.CrossReleaseConflictCount); err != nil {
		return ReleaseSummary{}, err
	}
	summary.ReadyToRelease = summary.TaskCount > 0 && summary.RequiredTaskCount == summary.RequiredCompletedCount && summary.CrossReleaseConflictCount == 0
	return summary, nil
}

func (s *Store) populateReleaseSummary(ctx context.Context, release *Release) error {
	summary, err := releaseSummaryTx(ctx, s.DB, release.ID)
	if err != nil {
		return err
	}
	release.Summary = summary
	return nil
}

// SetTaskRelease changes one nullable task membership while retaining the
// normal task version, claim, frozen-release, and activity contracts.
func (s *Store) SetTaskRelease(ctx context.Context, taskID string, releaseID *string, expected int64, actorID string) (Task, error) {
	input := TaskInput{ReleaseID: releaseID, ReleaseSet: true}
	return s.UpdateTask(ctx, taskID, input, expected, actorID)
}

func (s *Store) AssignTaskRelease(ctx context.Context, taskID, releaseID string, expected int64, actorID string) (Task, error) {
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return s.SetTaskRelease(ctx, taskID, nil, expected, actorID)
	}
	return s.SetTaskRelease(ctx, taskID, &releaseID, expected, actorID)
}

func (s *Store) ClearTaskRelease(ctx context.Context, taskID string, expected int64, actorID string) (Task, error) {
	return s.SetTaskRelease(ctx, taskID, nil, expected, actorID)
}
