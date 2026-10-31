package store

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Store struct {
	DB *sql.DB
}

func New(database *sql.DB) *Store { return &Store{DB: database} }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (*string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("timestamp must be RFC3339: %w", err)
	}
	v := t.UTC().Format(time.RFC3339Nano)
	return &v, nil
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func boolValue(value int) bool { return value != 0 }

func eventPayload(value any) string {
	if value == nil {
		return "{}"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func actorFromRow(scanner interface{ Scan(...any) error }) (Actor, error) {
	var a Actor
	var email, disabled sql.NullString
	var admin int
	if err := scanner.Scan(&a.ID, &a.Kind, &a.Name, &email, &admin, &disabled, &a.CreatedAt, &a.UpdatedAt, &a.Description); err != nil {
		return Actor{}, err
	}
	a.Email, a.DisabledAt, a.Admin = nullableString(email), nullableString(disabled), boolValue(admin)
	return a, nil
}

func projectFromRow(scanner interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var archived sql.NullString
	var favorite int
	if err := scanner.Scan(&p.ID, &p.Key, &p.Slug, &p.Name, &p.Description, &p.Color, &favorite, &archived, &p.ChecklistCompletionPolicy, &p.CreatedAt, &p.UpdatedAt, &p.Version, &p.SystemKind); err != nil {
		return Project{}, err
	}
	if p.ChecklistCompletionPolicy == "" {
		// Direct store tests and retained fixtures may omit the additive field.
		p.ChecklistCompletionPolicy = "warn"
	}
	p.Favorite, p.ArchivedAt = boolValue(favorite), nullableString(archived)
	return p, nil
}

func columnFromRow(scanner interface{ Scan(...any) error }) (Column, error) {
	var c Column
	var archived sql.NullString
	if err := scanner.Scan(&c.ID, &c.ProjectID, &c.Name, &c.SemanticState, &c.Position, &archived, &c.OrderingVersion, &c.CreatedAt, &c.UpdatedAt, &c.Version); err != nil {
		return Column{}, err
	}
	c.ArchivedAt = nullableString(archived)
	return c, nil
}

func labelFromRow(scanner interface{ Scan(...any) error }) (Label, error) {
	var l Label
	if err := scanner.Scan(&l.ID, &l.ProjectID, &l.Name, &l.Color, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return Label{}, err
	}
	return l, nil
}

func taskFromRow(scanner interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var assignee, claimed, claimExpiry, due, completed, parent, release sql.NullString
	if err := scanner.Scan(&t.ID, &t.Number, &t.ProjectID, &t.Kind, &t.ColumnID, &t.Title, &t.Description, &t.Priority, &t.Position, &assignee, &claimed, &claimExpiry, &due, &t.Version, &completed, &t.CreatedAt, &t.UpdatedAt, &parent, &release); err != nil {
		return Task{}, err
	}
	t.Key = "" // populated by callers because it requires a project key.
	t.Assignee, t.ClaimedBy, t.ClaimExpiresAt, t.DueAt, t.CompletedAt, t.ParentTaskID, t.ReleaseID = nullableString(assignee), nullableString(claimed), nullableString(claimExpiry), nullableString(due), nullableString(completed), nullableString(parent), nullableString(release)
	t.ParentID = t.ParentTaskID
	return t, nil
}

const taskColumns = `t.id, t.number, t.project_id, t.kind, t.column_id, t.title, t.description,
	t.priority, t.position, t.assignee_id, t.claimed_by, t.claim_expires_at, t.due_at,
	t.version, t.completed_at, t.created_at, t.updated_at, t.parent_task_id, t.release_id`

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func insertEvent(ctx context.Context, tx dependencySQL, eventType, actorID, projectID, taskID string, payload any) (int64, error) {
	id := newID()
	created := now()
	result, err := tx.ExecContext(ctx, `INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?)`, id, eventType, actorID, projectID, taskID, eventPayload(payload), created)
	if err != nil {
		return 0, err
	}
	cursor, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	// Notifications are an additive read model attached to the same event
	// transaction. A retained pre-notification binary can still write events
	// while this binary is rolling back/recovering against an older schema.
	if err := notifyForEventTx(ctx, tx, id, cursor, eventType, actorID, projectID, taskID, eventPayload(payload), created); err != nil && !isOptionalNotificationSchemaError(err) {
		return 0, err
	}
	return cursor, nil
}

func newID() string {
	// IDs are opaque and URL-safe. Randomness failure must fail closed: using a
	// timestamp as an ID would make security-sensitive identifiers predictable.
	buf := make([]byte, 16)
	if _, err := cryptorand.Read(buf); err == nil {
		return fmt.Sprintf("%x", buf)
	}
	panic("crypto/rand unavailable")
}
