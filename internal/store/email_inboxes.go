package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// EmailInbox is an address created in Helm whose mail becomes tickets in the
// ticket queue: <intake local part>+<Tag>@<intake domain>.
type EmailInbox struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	ProjectID      string  `json:"project_id"`
	ProjectKey     string  `json:"project_key"`
	ProjectSlug    string  `json:"project_slug"`
	AssigneeID     *string `json:"assignee_id,omitempty"`
	AssigneeName   string  `json:"assignee_name,omitempty"`
	Tag            string  `json:"tag"`
	CreatedAt      string  `json:"created_at"`
	ReplacedAt     *string `json:"replaced_at,omitempty"`
	DisabledAt     *string `json:"disabled_at,omitempty"`
	LastReceivedAt *string `json:"last_received_at,omitempty"`
	ReceivedCount  int     `json:"received_count"`
}

// EmailInboxInput creates an inbox. Its tickets land in the ticket queue
// (TicketQueue).
type EmailInboxInput struct {
	Name       string
	AssigneeID string
}

const emailInboxSelect = `SELECT i.id, i.name, i.project_id, p.key, p.slug, i.assignee_id, COALESCE(a.name, ''), i.tag, i.created_at, i.replaced_at, i.disabled_at, i.last_received_at, i.received_count FROM email_inboxes i JOIN projects p ON p.id = i.project_id LEFT JOIN actors a ON a.id = i.assignee_id`

func emailInboxFromRow(scanner interface{ Scan(...any) error }) (EmailInbox, error) {
	var inbox EmailInbox
	var assignee, replaced, disabled, last sql.NullString
	if err := scanner.Scan(&inbox.ID, &inbox.Name, &inbox.ProjectID, &inbox.ProjectKey, &inbox.ProjectSlug, &assignee, &inbox.AssigneeName, &inbox.Tag, &inbox.CreatedAt, &replaced, &disabled, &last, &inbox.ReceivedCount); err != nil {
		return EmailInbox{}, err
	}
	inbox.AssigneeID, inbox.ReplacedAt, inbox.DisabledAt, inbox.LastReceivedAt = nullableString(assignee), nullableString(replaced), nullableString(disabled), nullableString(last)
	return inbox, nil
}

var tagSlugNoise = regexp.MustCompile(`[^a-z0-9]+`)

// EmailInboxTagPattern is the shape of an inbox tag: a readable slug from
// the inbox name plus six random characters, so addresses are recognizable
// but not guessable.
var EmailInboxTagPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{2,38}[a-z0-9])$`)

func newInboxTag(name string) string {
	slug := strings.Trim(tagSlugNoise.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(slug) > 20 {
		slug = strings.TrimRight(slug[:20], "-")
	}
	if slug == "" {
		slug = "inbox"
	}
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable")
	}
	for index, value := range buf {
		buf[index] = emailTagAlphabet[int(value)%len(emailTagAlphabet)]
	}
	return slug + "-" + string(buf)
}

// CreateEmailInbox creates an inbox whose mail becomes tickets in the queue.
func (s *Store) CreateEmailInbox(ctx context.Context, input EmailInboxInput, actorID string) (EmailInbox, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 100 || strings.ContainsAny(input.Name, "\r\n\x00") {
		return EmailInbox{}, invalid("name must be 1-100 characters on one line", map[string]any{"field": "name"})
	}
	project, err := s.TicketQueue(ctx)
	if err != nil {
		return EmailInbox{}, err
	}
	if input.AssigneeID != "" {
		if _, err := s.resolveIntakeAssignee(ctx, input.AssigneeID); err != nil {
			return EmailInbox{}, invalid("assignee must be an enabled human", map[string]any{"field": "assignee"})
		}
	}
	id := newID()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO email_inboxes(id, name, project_id, assignee_id, tag, created_by, created_at) VALUES (?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), ?)`,
			id, input.Name, project.ID, input.AssigneeID, newInboxTag(input.Name), actorID, now()); err != nil {
			return err
		}
		_, err := insertEvent(ctx, tx, "email_inbox.created", actorID, project.ID, "", map[string]any{"email_inbox_id": id, "name": input.Name})
		return err
	})
	if err != nil {
		return EmailInbox{}, err
	}
	return s.GetEmailInbox(ctx, id)
}

func (s *Store) GetEmailInbox(ctx context.Context, id string) (EmailInbox, error) {
	inbox, err := emailInboxFromRow(s.DB.QueryRowContext(ctx, emailInboxSelect+` WHERE i.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return EmailInbox{}, notFound("inbox not found")
	}
	return inbox, err
}

// ListEmailInboxes returns enabled inboxes first, newest first.
func (s *Store) ListEmailInboxes(ctx context.Context) ([]EmailInbox, error) {
	rows, err := s.DB.QueryContext(ctx, emailInboxSelect+` ORDER BY i.disabled_at IS NOT NULL, i.created_at DESC, i.id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	inboxes := []EmailInbox{}
	for rows.Next() {
		inbox, err := emailInboxFromRow(rows)
		if err != nil {
			return nil, err
		}
		inboxes = append(inboxes, inbox)
	}
	return inboxes, rows.Err()
}

// ResolveEmailInbox finds the enabled inbox for an address tag.
func (s *Store) ResolveEmailInbox(ctx context.Context, tag string) (EmailInbox, error) {
	inbox, err := emailInboxFromRow(s.DB.QueryRowContext(ctx, emailInboxSelect+` WHERE i.tag = ? AND i.disabled_at IS NULL`, strings.ToLower(tag)))
	if errors.Is(err, sql.ErrNoRows) {
		return EmailInbox{}, notFound("inbox not found")
	}
	return inbox, err
}

// ReplaceEmailInboxAddress issues a new tag; the old address stops working.
func (s *Store) ReplaceEmailInboxAddress(ctx context.Context, id, actorID string) (EmailInbox, error) {
	return s.updateEmailInbox(ctx, id, actorID, "email_inbox.address_replaced", func(tx *sql.Tx, name string) error {
		_, err := tx.ExecContext(ctx, `UPDATE email_inboxes SET tag = ?, replaced_at = ? WHERE id = ?`, newInboxTag(name), now(), id)
		return err
	})
}

// DisableEmailInbox turns an inbox off; mail to it bounces. Tickets stay.
func (s *Store) DisableEmailInbox(ctx context.Context, id, actorID string) (EmailInbox, error) {
	return s.updateEmailInbox(ctx, id, actorID, "email_inbox.disabled", func(tx *sql.Tx, _ string) error {
		_, err := tx.ExecContext(ctx, `UPDATE email_inboxes SET disabled_at = ? WHERE id = ?`, now(), id)
		return err
	})
}

func (s *Store) updateEmailInbox(ctx context.Context, id, actorID, event string, change func(*sql.Tx, string) error) (EmailInbox, error) {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var projectID, name string
		if err := tx.QueryRowContext(ctx, `SELECT project_id, name FROM email_inboxes WHERE id = ? AND disabled_at IS NULL`, id).Scan(&projectID, &name); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("inbox not found")
			}
			return err
		}
		if err := change(tx, name); err != nil {
			return err
		}
		_, err := insertEvent(ctx, tx, event, actorID, projectID, "", map[string]any{"email_inbox_id": id})
		return err
	})
	if err != nil {
		return EmailInbox{}, err
	}
	return s.GetEmailInbox(ctx, id)
}
