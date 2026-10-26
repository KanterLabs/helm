package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
)

var (
	projectKeyPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_-]{0,15}$`)
	projectSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	colorPattern       = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

type ProjectInput struct {
	Key                       *string
	Slug                      *string
	Name                      *string
	Description               *string
	Color                     *string
	Favorite                  *bool
	Archived                  *bool
	ChecklistCompletionPolicy *string
}

type ColumnInput struct {
	Name          *string
	SemanticState *string
	Position      *int
	Archived      *bool
}

const projectSelect = `SELECT id, key, slug, name, description, color, favorite, archived_at, checklist_completion_policy, created_at, updated_at, version, COALESCE(system_kind, '') FROM projects`
const columnSelect = `SELECT id, project_id, name, semantic_state, position, archived_at, ordering_version, created_at, updated_at, version FROM columns`

func validateProjectInput(input ProjectInput, creating bool) (ProjectInput, error) {
	if creating && input.Name == nil {
		return ProjectInput{}, invalid("name is required", nil)
	}
	if creating && input.Key == nil {
		return ProjectInput{}, invalid("key is required", nil)
	}
	if input.Key != nil {
		value := strings.ToUpper(strings.TrimSpace(*input.Key))
		if !projectKeyPattern.MatchString(value) {
			return ProjectInput{}, invalid("key must be 1-16 uppercase letters, numbers, _ or - and start with a letter", nil)
		}
		input.Key = &value
	}
	if input.Slug != nil {
		value := strings.ToLower(strings.TrimSpace(*input.Slug))
		if !projectSlugPattern.MatchString(value) {
			return ProjectInput{}, invalid("slug must contain lowercase letters, numbers, and hyphens", nil)
		}
		input.Slug = &value
	}
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		if value == "" || len(value) > 200 {
			return ProjectInput{}, invalid("name must be between 1 and 200 characters", nil)
		}
		input.Name = &value
	}
	if input.Description != nil {
		value := strings.TrimSpace(*input.Description)
		if len(value) > 20000 {
			return ProjectInput{}, invalid("description is too long", nil)
		}
		input.Description = &value
	}
	if input.Color != nil {
		value := strings.TrimSpace(*input.Color)
		if !colorPattern.MatchString(value) {
			return ProjectInput{}, invalid("color must be a six-digit hexadecimal value", nil)
		}
		input.Color = &value
	}
	if input.ChecklistCompletionPolicy != nil {
		value := strings.ToLower(strings.TrimSpace(*input.ChecklistCompletionPolicy))
		if value != "warn" && value != "require" {
			return ProjectInput{}, invalid("checklist_completion_policy must be warn or require", nil)
		}
		input.ChecklistCompletionPolicy = &value
	}
	return input, nil
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *Store) CreateProject(ctx context.Context, input ProjectInput, actorID string) (Project, error) {
	validated, err := validateProjectInput(input, true)
	if err != nil {
		return Project{}, err
	}
	key := strings.ToUpper(strings.TrimSpace(*validated.Key))
	if !projectKeyPattern.MatchString(key) {
		return Project{}, invalid("key is required and must match the documented format", nil)
	}
	slug := ""
	if validated.Slug != nil {
		slug = *validated.Slug
	}
	if slug == "" {
		slug = slugify(*validated.Name)
	}
	if slug == "" {
		slug = strings.ToLower(key)
	}
	if !projectSlugPattern.MatchString(slug) {
		return Project{}, invalid("slug must match the documented format", nil)
	}
	description, color := "", "#64748b"
	if validated.Description != nil {
		description = *validated.Description
	}
	if validated.Color != nil && *validated.Color != "" {
		color = *validated.Color
	}
	favorite := false
	if validated.Favorite != nil {
		favorite = *validated.Favorite
	}
	id, created := newID(), now()
	checklistPolicy := "warn"
	if validated.ChecklistCompletionPolicy != nil {
		checklistPolicy = *validated.ChecklistCompletionPolicy
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO projects(id, key, slug, name, description, color, favorite, checklist_completion_policy, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, key, slug, *validated.Name, description, color, boolInt(favorite), checklistPolicy, created, created); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return &Error{Kind: ErrAlreadyExists, Message: "project key or slug already exists"}
			}
			return err
		}
		defaults := []struct{ name, state string }{
			{"Backlog", "backlog"}, {"Bugs", "backlog"}, {"Ready", "ready"}, {"In progress", "active"}, {"Blocked", "blocked"}, {"Done", "completed"},
		}
		for position, column := range defaults {
			if _, err := tx.ExecContext(ctx, `INSERT INTO columns(id, project_id, name, semantic_state, position, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, newID(), id, column.name, column.state, position, created, created); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_counters(project_id, next_number) VALUES (?, 1)`, id); err != nil {
			return err
		}
		_, err := insertEvent(ctx, tx, "project.created", actorID, id, "", map[string]any{"key": key, "name": *validated.Name, "checklist_completion_policy": checklistPolicy})
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, id)
}

func deriveKey(name string) string {
	parts := strings.Fields(strings.ToUpper(name))
	var b strings.Builder
	for _, part := range parts {
		for _, r := range part {
			if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
			if b.Len() >= 8 {
				break
			}
		}
		if b.Len() >= 8 {
			break
		}
	}
	if b.Len() == 0 {
		return "PROJECT"
	}
	return b.String()
}

func (s *Store) GetProject(ctx context.Context, reference string) (Project, error) {
	reference = strings.TrimSpace(reference)
	row := s.DB.QueryRowContext(ctx, projectSelect+` WHERE id = ? OR slug = ? OR key = ? LIMIT 1`, reference, strings.ToLower(reference), strings.ToUpper(reference))
	p, err := projectFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, notFound("project not found")
	}
	if err != nil {
		return Project{}, err
	}
	if err := s.populateProjectCounts(ctx, &p); err != nil {
		return Project{}, err
	}
	return p, nil
}

func (s *Store) ListProjects(ctx context.Context, limit, offset int) ([]Project, error) {
	return s.ListProjectsFiltered(ctx, limit, offset, ProjectFilter{})
}

func (s *Store) ListProjectsFiltered(ctx context.Context, limit, offset int, filter ProjectFilter) ([]Project, error) {
	return s.listProjectsFiltered(ctx, limit, offset, filter, false)
}

// ListProjectsFilteredWithExtra is used by cursor endpoints that need one
// sentinel row to determine whether a page has more results. The public page
// size remains capped at 200; this internal variant permits at most 201 rows.
func (s *Store) ListProjectsFilteredWithExtra(ctx context.Context, limit, offset int, filter ProjectFilter) ([]Project, error) {
	return s.listProjectsFiltered(ctx, limit, offset, filter, true)
}

func (s *Store) listProjectsFiltered(ctx context.Context, limit, offset int, filter ProjectFilter, allowExtra bool) ([]Project, error) {
	if limit <= 0 {
		limit = 50
	}
	maxLimit := 200
	if allowExtra {
		maxLimit++
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	query := projectSelect + ` WHERE 1=1`
	args := make([]any, 0, 4)
	if !filter.IncludeArchived {
		query += ` AND archived_at IS NULL`
	}
	if filter.FavoriteOnly {
		query += ` AND favorite = 1`
	}
	if filter.ProjectIDs != nil && len(filter.ProjectIDs) == 0 {
		query += ` AND 1=0`
	}
	if len(filter.ProjectIDs) > 0 {
		placeholders := make([]string, len(filter.ProjectIDs))
		for i, projectID := range filter.ProjectIDs {
			placeholders[i] = "?"
			args = append(args, projectID)
		}
		query += ` AND id IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY favorite DESC, lower(name), id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Project, 0)
	for rows.Next() {
		p, err := projectFromRow(rows)
		if err != nil {
			return nil, err
		}
		if err := s.populateProjectCounts(ctx, &p); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Store) populateProjectCounts(ctx context.Context, project *Project) error {
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks WHERE project_id = ? AND deleted_at IS NULL`, project.ID).Scan(&project.TaskCount); err != nil {
		return err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks t JOIN columns c ON c.id=t.column_id WHERE t.project_id = ? AND t.deleted_at IS NULL AND c.semantic_state='completed'`, project.ID).Scan(&project.CompletedCount); err != nil {
		return err
	}
	project.CompletedTaskCount = project.CompletedCount
	project.OpenTaskCount = project.TaskCount - project.CompletedCount
	if project.OpenTaskCount < 0 {
		project.OpenTaskCount = 0
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM columns WHERE project_id = ? AND archived_at IS NULL`, project.ID).Scan(&project.ColumnCount); err != nil {
		return err
	}
	return s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks WHERE project_id=? AND deleted_at IS NULL AND due_at IS NOT NULL AND due_at < ? AND completed_at IS NULL`, project.ID, now()).Scan(&project.OverdueTaskCount)
}

func (s *Store) UpdateProject(ctx context.Context, id string, input ProjectInput, actorID string) (Project, error) {
	return s.UpdateProjectWithVersion(ctx, id, input, actorID, nil)
}

// UpdateProjectWithVersion updates project metadata and, when expected is
// supplied, guards the mutation with the project's optimistic version. The
// legacy UpdateProject entry point intentionally remains unguarded for older
// internal callers; HTTP clients can opt into the stronger contract by
// sending If-Match.
func (s *Store) UpdateProjectWithVersion(ctx context.Context, id string, input ProjectInput, actorID string, expected *int64) (Project, error) {
	validated, err := validateProjectInput(input, false)
	if err != nil {
		return Project{}, err
	}
	if expected != nil && *expected <= 0 {
		return Project{}, ErrPrecondition
	}
	current, err := s.GetProject(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if current.SystemKind != "" && validated.Archived != nil && *validated.Archived {
		return Project{}, invalid("the ticket queue cannot be archived", map[string]any{"field": "archived"})
	}
	key, slug, name := current.Key, current.Slug, current.Name
	description, color := current.Description, current.Color
	favorite, archived := current.Favorite, current.ArchivedAt != nil
	if validated.Key != nil {
		key = *validated.Key
	}
	if validated.Slug != nil {
		slug = *validated.Slug
	}
	if validated.Name != nil {
		name = *validated.Name
	}
	if validated.Description != nil {
		description = *validated.Description
	}
	if validated.Color != nil {
		color = *validated.Color
	}
	if validated.Favorite != nil {
		favorite = *validated.Favorite
	}
	if validated.Archived != nil {
		archived = *validated.Archived
	}
	if !projectKeyPattern.MatchString(key) || !projectSlugPattern.MatchString(slug) {
		return Project{}, invalid("project key or slug is invalid", nil)
	}
	updated := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var archivedValue any
		if archived {
			archivedValue = updated
		}
		checklistPolicy := current.ChecklistCompletionPolicy
		if checklistPolicy == "" {
			checklistPolicy = "warn"
		}
		if validated.ChecklistCompletionPolicy != nil {
			checklistPolicy = *validated.ChecklistCompletionPolicy
		}
		query := `UPDATE projects SET key=?, slug=?, name=?, description=?, color=?, favorite=?, archived_at=?, checklist_completion_policy=?, version=version+1, updated_at=? WHERE id=?`
		args := []any{key, slug, name, description, color, boolInt(favorite), archivedValue, checklistPolicy, updated, current.ID}
		if expected != nil {
			query += ` AND version=?`
			args = append(args, *expected)
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return &Error{Kind: ErrAlreadyExists, Message: "project key or slug already exists"}
			}
			return err
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			if expected != nil {
				var currentVersion int64
				if versionErr := tx.QueryRowContext(ctx, `SELECT version FROM projects WHERE id=?`, current.ID).Scan(&currentVersion); errors.Is(versionErr, sql.ErrNoRows) {
					return ErrNotFound
				} else if versionErr != nil {
					return versionErr
				}
				return conflict("project has changed", map[string]any{"project_id": current.ID, "expected_version": *expected, "current_version": currentVersion})
			}
			return ErrNotFound
		}
		returnEvent := map[string]any{"key": key, "slug": slug, "name": name, "archived": archived, "checklist_completion_policy": checklistPolicy, "version": current.Version + 1}
		_, err = insertEvent(ctx, tx, "project.updated", actorID, current.ID, "", returnEvent)
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, current.ID)
}
