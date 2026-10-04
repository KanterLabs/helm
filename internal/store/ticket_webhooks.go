package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

// Ticket webhook input formats.
const (
	TicketWebhookGeneric = "generic"
	TicketWebhookCoolify = "coolify"
)

// TicketWebhook is an admin-managed inbound endpoint. The secret itself is
// never stored or returned after creation/rotation; SecretHint shows its last
// characters so operators can tell URLs apart.
type TicketWebhook struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Format         string  `json:"format"`
	ProjectID      string  `json:"project_id"`
	ProjectKey     string  `json:"project_key"`
	ProjectSlug    string  `json:"project_slug"`
	AssigneeID     *string `json:"assignee_id,omitempty"`
	SecretHint     string  `json:"secret_hint"`
	CreatedAt      string  `json:"created_at"`
	RotatedAt      *string `json:"rotated_at,omitempty"`
	DisabledAt     *string `json:"disabled_at,omitempty"`
	LastDeliveryAt *string `json:"last_delivery_at,omitempty"`
	DeliveryCount  int     `json:"delivery_count"`
}

// TicketWebhookInput creates a webhook. Its tickets land in the ticket
// queue (TicketQueue).
type TicketWebhookInput struct {
	Name       string
	Format     string
	AssigneeID string
}

const ticketWebhookSelect = `SELECT w.id, w.name, w.format, w.project_id, p.key, p.slug, w.assignee_id, w.secret_hint, w.created_at, w.rotated_at, w.disabled_at, w.last_delivery_at, w.delivery_count FROM ticket_webhooks w JOIN projects p ON p.id = w.project_id`

func ticketWebhookFromRow(scanner interface{ Scan(...any) error }) (TicketWebhook, error) {
	var hook TicketWebhook
	var assignee, rotated, disabled, last sql.NullString
	if err := scanner.Scan(&hook.ID, &hook.Name, &hook.Format, &hook.ProjectID, &hook.ProjectKey, &hook.ProjectSlug, &assignee, &hook.SecretHint, &hook.CreatedAt, &rotated, &disabled, &last, &hook.DeliveryCount); err != nil {
		return TicketWebhook{}, err
	}
	hook.AssigneeID, hook.RotatedAt, hook.DisabledAt, hook.LastDeliveryAt = nullableString(assignee), nullableString(rotated), nullableString(disabled), nullableString(last)
	return hook, nil
}

func newWebhookSecret() (secret, digest, hint string) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable")
	}
	secret = "hk_" + base64.RawURLEncoding.EncodeToString(buf)
	return secret, ticketWebhookDigest(secret), secret[len(secret)-4:]
}

func ticketWebhookDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func validateTicketWebhookInput(input TicketWebhookInput) (TicketWebhookInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 100 || strings.ContainsAny(input.Name, "\r\n\x00") {
		return input, invalid("name must be 1-100 characters on one line", map[string]any{"field": "name"})
	}
	if input.Format == "" {
		input.Format = TicketWebhookGeneric
	}
	if input.Format != TicketWebhookGeneric && input.Format != TicketWebhookCoolify {
		return input, invalid("format must be generic or coolify", map[string]any{"field": "format"})
	}
	return input, nil
}

// CreateTicketWebhook returns the webhook and its one-time plaintext secret.
func (s *Store) CreateTicketWebhook(ctx context.Context, input TicketWebhookInput, actorID string) (TicketWebhook, string, error) {
	input, err := validateTicketWebhookInput(input)
	if err != nil {
		return TicketWebhook{}, "", err
	}
	project, err := s.TicketQueue(ctx)
	if err != nil {
		return TicketWebhook{}, "", err
	}
	if input.AssigneeID != "" {
		if _, err := s.resolveIntakeAssignee(ctx, input.AssigneeID); err != nil {
			return TicketWebhook{}, "", invalid("assignee must be an enabled human", map[string]any{"field": "assignee"})
		}
	}
	id, created := newID(), now()
	secret, digest, hint := newWebhookSecret()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_webhooks(id, name, format, project_id, assignee_id, secret_sha256, secret_hint, created_by, created_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), ?)`, id, input.Name, input.Format, project.ID, input.AssigneeID, digest, hint, actorID, created); err != nil {
			return err
		}
		_, err := insertEvent(ctx, tx, "ticket_webhook.created", actorID, project.ID, "", map[string]any{"webhook_id": id, "name": input.Name, "format": input.Format})
		return err
	})
	if err != nil {
		return TicketWebhook{}, "", err
	}
	hook, err := s.GetTicketWebhook(ctx, id)
	return hook, secret, err
}

func (s *Store) GetTicketWebhook(ctx context.Context, id string) (TicketWebhook, error) {
	hook, err := ticketWebhookFromRow(s.DB.QueryRowContext(ctx, ticketWebhookSelect+` WHERE w.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return TicketWebhook{}, notFound("webhook not found")
	}
	return hook, err
}

// ListTicketWebhooks returns enabled webhooks first, newest first.
func (s *Store) ListTicketWebhooks(ctx context.Context) ([]TicketWebhook, error) {
	rows, err := s.DB.QueryContext(ctx, ticketWebhookSelect+` ORDER BY w.disabled_at IS NOT NULL, w.created_at DESC, w.id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hooks := []TicketWebhook{}
	for rows.Next() {
		hook, err := ticketWebhookFromRow(rows)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, hook)
	}
	return hooks, rows.Err()
}

// RotateTicketWebhook replaces the secret; the previous URL stops working
// immediately.
func (s *Store) RotateTicketWebhook(ctx context.Context, id, actorID string) (TicketWebhook, string, error) {
	secret, digest, hint := newWebhookSecret()
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var projectID string
		if err := tx.QueryRowContext(ctx, `UPDATE ticket_webhooks SET secret_sha256 = ?, secret_hint = ?, rotated_at = ? WHERE id = ? AND disabled_at IS NULL RETURNING project_id`, digest, hint, now(), id).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("webhook not found")
			}
			return err
		}
		_, err := insertEvent(ctx, tx, "ticket_webhook.rotated", actorID, projectID, "", map[string]any{"webhook_id": id})
		return err
	})
	if err != nil {
		return TicketWebhook{}, "", err
	}
	hook, err := s.GetTicketWebhook(ctx, id)
	return hook, secret, err
}

// DisableTicketWebhook permanently stops a webhook; existing tickets stay.
func (s *Store) DisableTicketWebhook(ctx context.Context, id, actorID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var projectID string
		if err := tx.QueryRowContext(ctx, `UPDATE ticket_webhooks SET disabled_at = ? WHERE id = ? AND disabled_at IS NULL RETURNING project_id`, now(), id).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("webhook not found")
			}
			return err
		}
		_, err := insertEvent(ctx, tx, "ticket_webhook.disabled", actorID, projectID, "", map[string]any{"webhook_id": id})
		return err
	})
}

// ResolveTicketWebhook finds the enabled webhook for a presented secret.
func (s *Store) ResolveTicketWebhook(ctx context.Context, secret string) (TicketWebhook, error) {
	hook, err := ticketWebhookFromRow(s.DB.QueryRowContext(ctx, ticketWebhookSelect+` WHERE w.secret_sha256 = ? AND w.disabled_at IS NULL`, ticketWebhookDigest(secret)))
	if errors.Is(err, sql.ErrNoRows) {
		return TicketWebhook{}, notFound("webhook not found")
	}
	return hook, err
}
