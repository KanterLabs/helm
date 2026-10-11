package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrIntakeRouting reports that operator-pinned intake routing (project,
// Backlog column or assignee) cannot currently be resolved. Callers must not
// fall back to another destination.
var ErrIntakeRouting = errors.New("intake_routing_unavailable")

const maxIntakeAlertsPerDelivery = 25

// AlertIntakeRoute is the trusted, configuration-derived destination for one
// integration. Nothing in it may come from alert content.
type AlertIntakeRoute struct {
	Integration string
	// ActorName labels the dedicated intake actor in Activity.
	ActorName string
	// ProjectRef is empty for the ticket queue (every current intake path).
	ProjectRef string
	// AssigneeRef is an enabled human's ID or email; empty leaves new
	// tickets unassigned.
	AssigneeRef string
	// WebhookID, when set, receives delivery statistics in the same
	// transaction.
	WebhookID string
	// Receipt, when set, makes a single-alert delivery idempotent per
	// message: a receipt already recorded returns "duplicate" and changes
	// nothing.
	Receipt *IntakeReceipt
	// InboxID, when set, receives email statistics in the same transaction.
	InboxID string
}

// IntakeAlert is one normalized condition parsed from an external delivery.
type IntakeAlert struct {
	AlertType    string
	FamilyKey    string
	ConditionKey string
	ResourceName string
	ResourceID   string
	Title        string
	Description  string
	Priority     string
	Evidence     map[string]string
	// ReopenAfterCompletion opens a new linked ticket when the family's
	// latest ticket is completed or deleted, instead of only recording the
	// repeat. ConditionKey is then ignored in favor of a fresh episode key.
	ReopenAfterCompletion bool
}

// AlertDisposition is the recorded outcome for one alert in a delivery.
type AlertDisposition struct {
	Disposition     string `json:"disposition"`
	ResourceName    string `json:"resource_name"`
	TaskID          string `json:"task_id,omitempty"`
	TaskKey         string `json:"task_key,omitempty"`
	OccurrenceCount int    `json:"occurrence_count"`
}

// AlertSource is the read-only external evidence embedded on a task created
// by alert intake.
type AlertSource struct {
	Integration     string            `json:"integration"`
	AlertType       string            `json:"alert_type"`
	ResourceName    string            `json:"resource_name"`
	ResourceID      string            `json:"resource_id"`
	Evidence        map[string]string `json:"evidence"`
	OccurrenceCount int               `json:"occurrence_count"`
	FirstReceivedAt string            `json:"first_received_at"`
	LastReceivedAt  string            `json:"last_received_at"`
	PreviousTaskID  *string           `json:"previous_task_id,omitempty"`
	PreviousTaskKey *string           `json:"previous_task_key,omitempty"`
}

func intakeActorID(integration string) string {
	return "actor-" + integration + "-intake"
}

func intakeRoutingError(message string) error {
	return &Error{Kind: ErrIntakeRouting, Message: message}
}

// IngestAlerts records every alert of one delivery in a single transaction.
// A new condition creates an assigned Backlog task; a known condition only
// increments its occurrence count and never edits, reopens or recreates the
// task. Either every alert is recorded or none is.
func (s *Store) IngestAlerts(ctx context.Context, route AlertIntakeRoute, alerts []IntakeAlert) ([]AlertDisposition, error) {
	if len(alerts) == 0 || len(alerts) > maxIntakeAlertsPerDelivery {
		return nil, invalid(fmt.Sprintf("a delivery must contain 1-%d alerts", maxIntakeAlertsPerDelivery), nil)
	}
	var project Project
	var err error
	if strings.TrimSpace(route.ProjectRef) == "" {
		project, err = s.TicketQueue(ctx)
	} else {
		project, err = s.GetProject(ctx, route.ProjectRef)
	}
	if errors.Is(err, ErrNotFound) {
		return nil, intakeRoutingError("intake project is not available")
	}
	if err != nil {
		return nil, err
	}
	column, err := s.StateColumn(ctx, project.ID, "backlog")
	if errors.Is(err, ErrNotFound) {
		return nil, intakeRoutingError("intake project has no Backlog column")
	}
	if err != nil {
		return nil, err
	}
	assigneeID := ""
	if strings.TrimSpace(route.AssigneeRef) != "" {
		assignee, err := s.resolveIntakeAssignee(ctx, route.AssigneeRef)
		if err != nil {
			return nil, err
		}
		assigneeID = assignee.ID
	}
	actorID := intakeActorID(route.Integration)
	if route.Receipt != nil && len(alerts) != 1 {
		return nil, invalid("a receipt covers exactly one alert", nil)
	}
	results := make([]AlertDisposition, 0, len(alerts))
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		results = results[:0]
		// Writing first takes SQLite's writer lock before any read, so
		// concurrent deliveries serialize instead of racing on correlation.
		created := now()
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(id, kind, name, admin, created_at, updated_at, description) VALUES (?, 'agent', ?, 0, ?, ?, ?)`, actorID, route.ActorName, created, created, "Records external alerts. Holds no credentials and cannot sign in."); err != nil {
			return err
		}
		receiptID := ""
		if route.Receipt != nil {
			id, prior, err := claimEmailReceiptTx(ctx, tx, *route.Receipt, route.InboxID, created)
			if err != nil {
				return err
			}
			if prior != nil {
				prior.ResourceName = alerts[0].ResourceName
				results = append(results, *prior)
				return nil
			}
			receiptID = id
		}
		for _, alert := range alerts {
			result, err := ingestAlertTx(ctx, tx, route.Integration, actorID, project, column.ID, assigneeID, alert, created)
			if err != nil {
				return err
			}
			results = append(results, result)
		}
		if receiptID != "" {
			if err := completeEmailReceiptTx(ctx, tx, receiptID, results[0]); err != nil {
				return err
			}
		}
		if route.WebhookID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE ticket_webhooks SET delivery_count = delivery_count + 1, last_delivery_at = ? WHERE id = ?`, created, route.WebhookID); err != nil {
				return err
			}
		}
		if route.InboxID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE email_inboxes SET received_count = received_count + 1, last_received_at = ? WHERE id = ?`, created, route.InboxID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Store) resolveIntakeAssignee(ctx context.Context, reference string) (Actor, error) {
	actor, err := s.GetActor(ctx, reference)
	if errors.Is(err, ErrNotFound) {
		actor, err = s.FindActorByEmail(ctx, reference)
	}
	if errors.Is(err, ErrNotFound) {
		return Actor{}, intakeRoutingError("intake assignee is not available")
	}
	if err != nil {
		return Actor{}, err
	}
	if actor.Kind != "human" || actor.DisabledAt != nil {
		return Actor{}, intakeRoutingError("intake assignee must be an enabled human")
	}
	return actor, nil
}

func ingestAlertTx(ctx context.Context, tx *sql.Tx, integration, actorID string, project Project, columnID, assigneeID string, alert IntakeAlert, received string) (AlertDisposition, error) {
	evidence, err := json.Marshal(alert.Evidence)
	if err != nil {
		return AlertDisposition{}, err
	}
	if alert.ReopenAfterCompletion {
		var openID string
		var openTask sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT c.id, c.task_id FROM alert_conditions c JOIN tasks t ON t.id = c.task_id
			WHERE c.integration = ? AND c.family_key = ? AND t.deleted_at IS NULL AND t.completed_at IS NULL
			ORDER BY c.first_received_at DESC, c.rowid DESC LIMIT 1`, integration, alert.FamilyKey).Scan(&openID, &openTask)
		if err == nil {
			var count int
			if err := tx.QueryRowContext(ctx, `UPDATE alert_conditions SET occurrence_count = occurrence_count + 1, last_received_at = ? WHERE id = ? RETURNING occurrence_count`, received, openID).Scan(&count); err != nil {
				return AlertDisposition{}, err
			}
			return recordRepeatAlertTx(ctx, tx, integration, actorID, openTask, AlertDisposition{ResourceName: alert.ResourceName, OccurrenceCount: count})
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return AlertDisposition{}, err
		}
		alert.ConditionKey = alert.FamilyKey + "#" + newID()
	}
	var conditionID string
	var taskID sql.NullString
	var count int
	err = tx.QueryRowContext(ctx, `INSERT INTO alert_conditions(id, integration, alert_type, family_key, condition_key, project_id, resource_name, resource_id, evidence, occurrence_count, first_received_at, last_received_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(integration, condition_key) DO UPDATE SET occurrence_count = occurrence_count + 1, last_received_at = excluded.last_received_at
		RETURNING id, task_id, occurrence_count`,
		newID(), integration, alert.AlertType, alert.FamilyKey, alert.ConditionKey, project.ID, alert.ResourceName, alert.ResourceID, string(evidence), received, received,
	).Scan(&conditionID, &taskID, &count)
	if err != nil {
		return AlertDisposition{}, err
	}
	result := AlertDisposition{ResourceName: alert.ResourceName, OccurrenceCount: count}
	if count > 1 {
		return recordRepeatAlertTx(ctx, tx, integration, actorID, taskID, result)
	}
	var previousID, previousTaskID, previousKey sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT c.id, t.id, p.key || '-' || t.number
		FROM alert_conditions c
		LEFT JOIN tasks t ON t.id = c.task_id AND t.deleted_at IS NULL
		LEFT JOIN projects p ON p.id = t.project_id
		WHERE c.integration = ? AND c.family_key = ? AND c.id <> ?
		ORDER BY c.first_received_at DESC, c.rowid DESC LIMIT 1`, integration, alert.FamilyKey, conditionID).Scan(&previousID, &previousTaskID, &previousKey)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AlertDisposition{}, err
	}
	description := alert.Description
	if previousKey.Valid {
		description += "\n\nFollows earlier alert " + previousKey.String + "."
	}
	id, number, err := insertIntakeTaskTx(ctx, tx, integration, actorID, project, columnID, assigneeID, alert, description, received)
	if err != nil {
		return AlertDisposition{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE alert_conditions SET task_id = ?, previous_condition_id = ? WHERE id = ?`, id, nullableID(previousID), conditionID); err != nil {
		return AlertDisposition{}, err
	}
	result.Disposition = "created"
	result.TaskID = id
	result.TaskKey = fmt.Sprintf("%s-%d", project.Key, number)
	return result, nil
}

// recordRepeatAlertTx leaves the task row untouched: no version bump, no
// field change and no notification, so a human edit in progress is never
// invalidated by a repeated notice.
func recordRepeatAlertTx(ctx context.Context, tx *sql.Tx, integration, actorID string, taskID sql.NullString, result AlertDisposition) (AlertDisposition, error) {
	result.Disposition = "retained"
	if !taskID.Valid {
		return result, nil
	}
	var projectID, projectKey string
	var number int
	var completedAt, deletedAt sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT t.project_id, p.key, t.number, t.completed_at, t.deleted_at FROM tasks t JOIN projects p ON p.id = t.project_id WHERE t.id = ?`, taskID.String).Scan(&projectID, &projectKey, &number, &completedAt, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return AlertDisposition{}, err
	}
	if deletedAt.Valid {
		return result, nil
	}
	result.TaskID = taskID.String
	result.TaskKey = fmt.Sprintf("%s-%d", projectKey, number)
	if !completedAt.Valid {
		result.Disposition = "repeated"
	}
	payload := map[string]any{
		"summary":          fmt.Sprintf("Occurrence %d", result.OccurrenceCount),
		"occurrence_count": result.OccurrenceCount,
		"source":           integration,
	}
	if _, err := insertEvent(ctx, tx, "alert.repeated", actorID, projectID, taskID.String, payload); err != nil {
		return AlertDisposition{}, err
	}
	return result, nil
}

func insertIntakeTaskTx(ctx context.Context, tx *sql.Tx, integration, actorID string, project Project, columnID, assigneeID string, alert IntakeAlert, description, created string) (string, int, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_counters(project_id, next_number) VALUES (?, 2) ON CONFLICT(project_id) DO UPDATE SET next_number = project_counters.next_number + 1`, project.ID); err != nil {
		return "", 0, err
	}
	state, err := authoritativeColumnStateTx(ctx, tx, columnID, project.ID)
	if err != nil {
		return "", 0, err
	}
	if state != "backlog" {
		return "", 0, intakeRoutingError("intake Backlog column changed during delivery")
	}
	var number int
	if err := tx.QueryRowContext(ctx, `SELECT next_number - 1 FROM project_counters WHERE project_id = ?`, project.ID).Scan(&number); err != nil {
		return "", 0, err
	}
	var position float64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position)+1, 0) FROM tasks WHERE column_id = ? AND deleted_at IS NULL`, columnID).Scan(&position); err != nil {
		return "", 0, err
	}
	id := newID()
	if err := txExecTaskCreate(ctx, tx, id, project.ID, number, columnID, defaultTaskKind, alert.Title, description, alert.Priority, position, assigneeID, "", nil, created, "", ""); err != nil {
		return "", 0, err
	}
	if err := insertTicketTx(ctx, tx, id, "alert", created); err != nil {
		return "", 0, err
	}
	// task.created with an assignee produces exactly one assignment
	// notification through the recipient's normal preferences.
	if _, err := insertEvent(ctx, tx, "task.created", actorID, project.ID, id, map[string]any{"number": number, "source": integration}); err != nil {
		return "", 0, err
	}
	return id, number, nil
}

func nullableID(value sql.NullString) any {
	if value.Valid && strings.TrimSpace(value.String) != "" {
		return value.String
	}
	return nil
}

func (s *Store) populateAlertSource(ctx context.Context, task *Task) error {
	var source AlertSource
	var evidence string
	var previousTaskID, previousKey sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT c.integration, c.alert_type, c.resource_name, c.resource_id, c.evidence, c.occurrence_count, c.first_received_at, c.last_received_at, pt.id, pp.key || '-' || pt.number
		FROM alert_conditions c
		LEFT JOIN alert_conditions prev ON prev.id = c.previous_condition_id
		LEFT JOIN tasks pt ON pt.id = prev.task_id AND pt.deleted_at IS NULL
		LEFT JOIN projects pp ON pp.id = pt.project_id
		WHERE c.task_id = ?`, task.ID).Scan(&source.Integration, &source.AlertType, &source.ResourceName, &source.ResourceID, &evidence, &source.OccurrenceCount, &source.FirstReceivedAt, &source.LastReceivedAt, &previousTaskID, &previousKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	source.Evidence = map[string]string{}
	if err := json.Unmarshal([]byte(evidence), &source.Evidence); err != nil {
		return err
	}
	source.PreviousTaskID = nullableString(previousTaskID)
	source.PreviousTaskKey = nullableString(previousKey)
	task.AlertSource = &source
	return nil
}
