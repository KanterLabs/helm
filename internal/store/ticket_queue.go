package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// The ticket queue (docs/TICKET_QUEUE_PLAN.md): every new ticket lands in one
// built-in project, and triage optionally files it into a real project.

// SystemKindTickets marks the built-in ticket queue project.
const SystemKindTickets = "tickets"

var (
	ticketQueueKeys  = []string{"TKT", "TICKET", "TICKETS", "TKTQ", "HELMTKT"}
	ticketQueueSlugs = []string{"tickets", "ticket-queue", "helm-tickets"}
)

// LookupTicketQueue returns the ticket queue if it exists, without creating it.
func (s *Store) LookupTicketQueue(ctx context.Context) (Project, bool, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM projects WHERE system_kind = ?`, SystemKindTickets).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, false, nil
	}
	if err != nil {
		return Project{}, false, err
	}
	project, err := s.GetProject(ctx, id)
	return project, err == nil, err
}

// TicketQueue returns the built-in ticket queue, creating it on first use.
// Creating it also points existing webhooks and inboxes at it, so every
// intake path delivers to the one queue from then on.
func (s *Store) TicketQueue(ctx context.Context) (Project, error) {
	for attempt := 0; attempt < 2; attempt++ {
		project, found, err := s.LookupTicketQueue(ctx)
		if err != nil || found {
			return project, err
		}
		if err := s.createTicketQueue(ctx); err != nil && !isUniqueViolation(err) {
			return Project{}, err
		}
		// A concurrent first use may have won the unique index; read again.
	}
	return Project{}, errors.New("ticket queue could not be created")
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func (s *Store) createTicketQueue(ctx context.Context) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		key, err := firstFreeProjectValue(ctx, tx, `SELECT 1 FROM projects WHERE upper(key) = ?`, ticketQueueKeys)
		if err != nil {
			return err
		}
		slug, err := firstFreeProjectValue(ctx, tx, `SELECT 1 FROM projects WHERE lower(slug) = lower(?)`, ticketQueueSlugs)
		if err != nil {
			return err
		}
		id, created := newID(), now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO projects(id, key, slug, name, description, color, favorite, checklist_completion_policy, created_at, updated_at, system_kind) VALUES (?, ?, ?, 'Tickets', 'Every new ticket lands here for triage.', '#7c3aed', 0, 'warn', ?, ?, ?)`, id, key, slug, created, created, SystemKindTickets); err != nil {
			return err
		}
		columns := []struct{ name, state string }{
			{"Needs triage", "backlog"}, {"Ready", "ready"}, {"In progress", "active"}, {"Waiting", "blocked"}, {"Done", "completed"},
		}
		for position, column := range columns {
			if _, err := tx.ExecContext(ctx, `INSERT INTO columns(id, project_id, name, semantic_state, position, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, newID(), id, column.name, column.state, position, created, created); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_counters(project_id, next_number) VALUES (?, 1)`, id); err != nil {
			return err
		}
		for _, statement := range []string{`UPDATE ticket_webhooks SET project_id = ?`, `UPDATE email_inboxes SET project_id = ?`} {
			if _, err := tx.ExecContext(ctx, statement, id); err != nil {
				return err
			}
		}
		_, err = insertEvent(ctx, tx, "project.created", "", id, "", map[string]any{"key": key, "name": "Tickets", "system_kind": SystemKindTickets})
		return err
	})
}

func firstFreeProjectValue(ctx context.Context, tx *sql.Tx, query string, candidates []string) (string, error) {
	for _, candidate := range candidates {
		var taken int
		err := tx.QueryRowContext(ctx, query, candidate).Scan(&taken)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return candidates[0] + strings.ToUpper(newID()[:4]), nil
}

// FileTicket moves a ticket into a project: it gets that project's next
// number, a column with the same status, matching labels (created by name
// when missing) and keeps its comments, evidence, watches and repeat count.
// The old key keeps resolving to it.
func (s *Store) FileTicket(ctx context.Context, taskID, projectRef, actorID string) (Task, error) {
	destination, err := s.GetProject(ctx, projectRef)
	if errors.Is(err, ErrNotFound) {
		return Task{}, invalid("project not found", map[string]any{"field": "project"})
	}
	if err != nil {
		return Task{}, err
	}
	if destination.SystemKind != "" || destination.ArchivedAt != nil {
		return Task{}, invalid("choose an active project", map[string]any{"field": "project"})
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var sourceProject, sourceKey, columnID, state string
		var number int
		var parent, release, claimedBy, claimExpires sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT t.project_id, p.key, t.number, t.column_id, c.semantic_state, t.parent_task_id, t.release_id, t.claimed_by, t.claim_expires_at
			FROM tasks t JOIN projects p ON p.id = t.project_id JOIN columns c ON c.id = t.column_id
			JOIN tickets k ON k.task_id = t.id
			WHERE t.id = ? AND t.deleted_at IS NULL`, taskID).Scan(&sourceProject, &sourceKey, &number, &columnID, &state, &parent, &release, &claimedBy, &claimExpires)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("ticket not found")
		}
		if err != nil {
			return err
		}
		if sourceProject == destination.ID {
			return invalid("the ticket is already in "+destination.Key, map[string]any{"field": "project"})
		}
		if claimedBy.Valid && claimExpires.Valid && claimExpires.String > now() {
			return conflict("an agent is working on this ticket; file it after the claim ends", nil)
		}
		var blocker string
		if err := tx.QueryRowContext(ctx, `SELECT CASE
				WHEN ? IS NOT NULL THEN 'it is a subtask'
				WHEN EXISTS (SELECT 1 FROM tasks WHERE parent_task_id = ? AND deleted_at IS NULL) THEN 'it has subtasks'
				WHEN EXISTS (SELECT 1 FROM task_dependencies WHERE task_id = ? OR prerequisite_task_id = ?) THEN 'it has dependencies'
				WHEN EXISTS (SELECT 1 FROM releases WHERE id = ? AND released_at IS NOT NULL) THEN 'it is in a shipped release'
				ELSE '' END`, parent, taskID, taskID, taskID, release).Scan(&blocker); err != nil {
			return err
		}
		if blocker != "" {
			return conflict("this ticket cannot be filed because "+blocker, nil)
		}
		target, err := fileDestinationColumnTx(ctx, tx, destination.ID, state)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_counters(project_id, next_number) VALUES (?, 2) ON CONFLICT(project_id) DO UPDATE SET next_number = project_counters.next_number + 1`, destination.ID); err != nil {
			return err
		}
		var newNumber int
		if err := tx.QueryRowContext(ctx, `SELECT next_number - 1 FROM project_counters WHERE project_id = ?`, destination.ID).Scan(&newNumber); err != nil {
			return err
		}
		labels, err := fileTicketLabelsTx(ctx, tx, taskID, destination.ID)
		if err != nil {
			return err
		}
		updated := now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_number_aliases(project_id, number, task_id, created_at) VALUES (?, ?, ?, ?)`, sourceProject, number, taskID, updated); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET project_id = ?, number = ?, column_id = ?, release_id = NULL, claimed_by = NULL, claim_expires_at = NULL,
			position = (SELECT COALESCE(MAX(position) + 1, 0) FROM tasks WHERE column_id = ? AND deleted_at IS NULL),
			version = version + 1, updated_at = ? WHERE id = ?`, destination.ID, newNumber, target, target, updated, taskID); err != nil {
			return err
		}
		if err := replaceTaskLabels(ctx, tx, taskID, destination.ID, labels); err != nil {
			return err
		}
		for _, statement := range []string{`UPDATE alert_conditions SET project_id = ? WHERE task_id = ?`, `UPDATE watches SET project_id = ? WHERE task_id = ?`} {
			if _, err := tx.ExecContext(ctx, statement, destination.ID, taskID); err != nil {
				return err
			}
		}
		oldKey, newKey := fmt.Sprintf("%s-%d", sourceKey, number), fmt.Sprintf("%s-%d", destination.Key, newNumber)
		_, err = insertEvent(ctx, tx, "ticket.filed", actorID, destination.ID, taskID, map[string]any{"from_key": oldKey, "to_key": newKey, "from_project_id": sourceProject, "to_project_id": destination.ID})
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, taskID)
}

// fileDestinationColumnTx keeps the ticket's status: the destination's first
// live column with the same state, else its Backlog.
func fileDestinationColumnTx(ctx context.Context, tx *sql.Tx, projectID, state string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM columns WHERE project_id = ? AND archived_at IS NULL AND semantic_state IN (?, 'backlog')
		ORDER BY semantic_state = ? DESC, position, id LIMIT 1`, projectID, state, state).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", invalid("that project has no column for this ticket's status or a Backlog column", map[string]any{"field": "project"})
	}
	return id, err
}

// fileTicketLabelsTx returns the ticket's label names, creating any the
// destination project lacks (same name and color).
func fileTicketLabelsTx(ctx context.Context, tx *sql.Tx, taskID, projectID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT l.name, l.color FROM task_labels tl JOIN labels l ON l.id = tl.label_id WHERE tl.task_id = ? ORDER BY l.name`, taskID)
	if err != nil {
		return nil, err
	}
	type label struct{ name, color string }
	var found []label
	for rows.Next() {
		var item label
		if err := rows.Scan(&item.name, &item.color); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(found))
	for _, item := range found {
		created := now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO labels(id, project_id, name, color, created_at, updated_at) SELECT ?, ?, ?, ?, ?, ?
			WHERE NOT EXISTS (SELECT 1 FROM labels WHERE project_id = ? AND lower(name) = lower(?))`, newID(), projectID, item.name, item.color, created, created, projectID, item.name); err != nil {
			return nil, err
		}
		names = append(names, item.name)
	}
	return names, nil
}

// resolveTaskAlias finds a task by a key it had before being filed.
func (s *Store) resolveTaskAlias(ctx context.Context, reference string) (string, bool, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT a.task_id FROM task_number_aliases a JOIN projects p ON p.id = a.project_id
		WHERE lower(p.key || '-' || CAST(a.number AS TEXT)) = lower(?) LIMIT 1`, strings.TrimSpace(reference)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}
