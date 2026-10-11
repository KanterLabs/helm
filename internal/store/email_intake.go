package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
)

// EmailIntake is the Cloudflare Email Routing → Worker → Helm pipeline that
// gives every ticket webhook an email address. It never holds a credential:
// the Worker's intake secret and webhook address tags are stored as SHA-256.
type EmailIntake struct {
	ID                      string  `json:"id"`
	Provider                string  `json:"provider"`
	Domain                  string  `json:"domain"`
	LocalPart               string  `json:"local_part"`
	AccountID               string  `json:"account_id"`
	ZoneID                  string  `json:"zone_id"`
	WorkerName              string  `json:"worker_name"`
	RuleID                  string  `json:"rule_id"`
	FallbackAddress         string  `json:"fallback_address,omitempty"`
	SubaddressEnabledByHelm bool    `json:"subaddress_enabled_by_helm"`
	PublicEndpointID        string  `json:"public_endpoint_id,omitempty"`
	Status                  string  `json:"status"`
	CleanupPending          bool    `json:"cleanup_pending"`
	CreatedAt               string  `json:"created_at"`
	CreatedByName           string  `json:"created_by_name,omitempty"`
	DisabledAt              *string `json:"disabled_at,omitempty"`
}

// Address is the base address the Cloudflare rule matches.
func (e EmailIntake) Address() string { return e.LocalPart + "@" + e.Domain }

// InboxAddress is the full address for an inbox tag.
func (e EmailIntake) InboxAddress(tag string) string {
	return e.LocalPart + "+" + tag + "@" + e.Domain
}

// EmailReceipt is one received message (or refusal) shown under Recent
// emails. Sender and subject are display copies, already bounded.
type EmailReceipt struct {
	ID              string `json:"id"`
	InboxID         string `json:"inbox_id,omitempty"`
	InboxName       string `json:"inbox_name,omitempty"`
	Sender          string `json:"sender"`
	Subject         string `json:"subject"`
	Outcome         string `json:"outcome"`
	TaskKey         string `json:"task_key,omitempty"`
	ProjectSlug     string `json:"project_slug,omitempty"`
	OccurrenceCount int    `json:"occurrence_count"`
	Reason          string `json:"reason,omitempty"`
	ReceivedAt      string `json:"received_at"`
}

// IntakeReceipt makes an alert delivery idempotent per received message.
// IngestAlerts records it in the same transaction as the ticket.
type IntakeReceipt struct {
	IntakeID string
	// Key identifies the message (normalized Message-ID, or a digest of
	// the raw message); only its SHA-256 is stored.
	Key     string
	Sender  string
	Subject string
}

// Email receipt outcomes that are refusals rather than tickets.
const (
	EmailOutcomeUnknownRecipient = "unknown_recipient"
	EmailOutcomeUnreadable       = "unreadable"
	EmailOutcomeTooLarge         = "too_large"
)

const maxEmailReceiptsPerIntake = 500

const emailIntakeSelect = `SELECT e.id, e.provider, e.domain, e.local_part, e.account_id, e.zone_id, e.worker_name, e.rule_id, COALESCE(e.fallback_address, ''), e.subaddress_enabled_by_helm, COALESCE(e.public_endpoint_id, ''), e.status, e.cleanup_pending, e.created_at, COALESCE(a.name, ''), e.disabled_at FROM email_intakes e LEFT JOIN actors a ON a.id = e.created_by`

func emailIntakeFromRow(scanner interface{ Scan(...any) error }) (EmailIntake, error) {
	var intake EmailIntake
	var subaddress, cleanup int
	var disabled sql.NullString
	if err := scanner.Scan(&intake.ID, &intake.Provider, &intake.Domain, &intake.LocalPart, &intake.AccountID, &intake.ZoneID, &intake.WorkerName, &intake.RuleID, &intake.FallbackAddress, &subaddress, &intake.PublicEndpointID, &intake.Status, &cleanup, &intake.CreatedAt, &intake.CreatedByName, &disabled); err != nil {
		return EmailIntake{}, err
	}
	intake.SubaddressEnabledByHelm, intake.CleanupPending, intake.DisabledAt = subaddress == 1, cleanup == 1, nullableString(disabled)
	return intake, nil
}

func secretDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// ActiveEmailIntake returns the single active intake, if any.
func (s *Store) ActiveEmailIntake(ctx context.Context) (EmailIntake, bool, error) {
	intake, err := emailIntakeFromRow(s.DB.QueryRowContext(ctx, emailIntakeSelect+` WHERE e.status = 'active'`))
	if errors.Is(err, sql.ErrNoRows) {
		return EmailIntake{}, false, nil
	}
	return intake, err == nil, err
}

func (s *Store) GetEmailIntake(ctx context.Context, id string) (EmailIntake, error) {
	intake, err := emailIntakeFromRow(s.DB.QueryRowContext(ctx, emailIntakeSelect+` WHERE e.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return EmailIntake{}, notFound("email intake not found")
	}
	return intake, err
}

func (s *Store) ListEmailIntakes(ctx context.Context) ([]EmailIntake, error) {
	rows, err := s.DB.QueryContext(ctx, emailIntakeSelect+` ORDER BY e.status = 'active' DESC, e.created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intakes := []EmailIntake{}
	for rows.Next() {
		intake, err := emailIntakeFromRow(rows)
		if err != nil {
			return nil, err
		}
		intakes = append(intakes, intake)
	}
	return intakes, rows.Err()
}

// ResolveEmailIntake finds the active intake for the Worker's secret.
func (s *Store) ResolveEmailIntake(ctx context.Context, secret string) (EmailIntake, error) {
	intake, err := emailIntakeFromRow(s.DB.QueryRowContext(ctx, emailIntakeSelect+` WHERE e.secret_sha256 = ? AND e.status = 'active'`, secretDigest(secret)))
	if errors.Is(err, sql.ErrNoRows) {
		return EmailIntake{}, notFound("email intake not found")
	}
	return intake, err
}

// CreateEmailIntake stores a provisioned intake as the active one.
func (s *Store) CreateEmailIntake(ctx context.Context, intake EmailIntake, secret, actorID string) (EmailIntake, error) {
	if intake.ID == "" {
		intake.ID = newID()
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO email_intakes(id, provider, domain, local_part, account_id, zone_id, worker_name, rule_id, secret_sha256, fallback_address, subaddress_enabled_by_helm, public_endpoint_id, status, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), 'active', NULLIF(?, ''), ?)`,
			intake.ID, intake.Provider, intake.Domain, intake.LocalPart, intake.AccountID, intake.ZoneID, intake.WorkerName, intake.RuleID, secretDigest(secret), intake.FallbackAddress, boolInt(intake.SubaddressEnabledByHelm), intake.PublicEndpointID, actorID, now()); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return conflict("email intake is already active; remove it first", nil)
			}
			return err
		}
		_, err := insertEvent(ctx, tx, "email_intake.created", actorID, "", "", map[string]any{"email_intake_id": intake.ID, "address": intake.Address()})
		return err
	})
	if err != nil {
		return EmailIntake{}, err
	}
	return s.GetEmailIntake(ctx, intake.ID)
}

// DisableEmailIntake marks the intake disabled; cleanupPending records that
// its Worker and rule still exist in Cloudflare.
func (s *Store) DisableEmailIntake(ctx context.Context, id, actorID string, cleanupPending bool) (EmailIntake, error) {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE email_intakes SET status = 'disabled', disabled_at = COALESCE(disabled_at, ?), cleanup_pending = ? WHERE id = ?`, now(), boolInt(cleanupPending), id)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return notFound("email intake not found")
		}
		_, err = insertEvent(ctx, tx, "email_intake.disabled", actorID, "", "", map[string]any{"email_intake_id": id, "cleanup_pending": cleanupPending})
		return err
	})
	if err != nil {
		return EmailIntake{}, err
	}
	return s.GetEmailIntake(ctx, id)
}

// emailTagAlphabet is lowercase RFC 4648 base32: safe in any mailbox and
// immune to case folding by mail servers.
const emailTagAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

// RecordEmailRefusal lists a refused message under Recent emails. A refusal
// already recorded for the same message is kept as is. Refusals use their
// own receipt namespace, so a message refused once (say, sent to a replaced
// address) is still accepted if it later arrives at a valid one.
func (s *Store) RecordEmailRefusal(ctx context.Context, receipt IntakeReceipt, inboxID, outcome, reason string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO email_receipts(id, intake_id, receipt_sha256, inbox_id, sender, subject, outcome, reason, received_at) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?) ON CONFLICT(intake_id, receipt_sha256) DO NOTHING`,
			newID(), receipt.IntakeID, secretDigest("refused:"+outcome+":"+receipt.Key), inboxID, receipt.Sender, receipt.Subject, outcome, reason, now()); err != nil {
			return err
		}
		return pruneEmailReceiptsTx(ctx, tx, receipt.IntakeID)
	})
}

func pruneEmailReceiptsTx(ctx context.Context, tx *sql.Tx, intakeID string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM email_receipts WHERE intake_id = ? AND id NOT IN (SELECT id FROM email_receipts WHERE intake_id = ? ORDER BY received_at DESC, rowid DESC LIMIT ?)`, intakeID, intakeID, maxEmailReceiptsPerIntake)
	return err
}

// RecentEmailReceipts lists the newest receipts for an intake.
func (s *Store) RecentEmailReceipts(ctx context.Context, intakeID string, limit int) ([]EmailReceipt, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id, COALESCE(r.inbox_id, ''), COALESCE(i.name, ''), r.sender, r.subject, r.outcome, COALESCE(r.task_key, ''), COALESCE(p.slug, ''), r.occurrence_count, COALESCE(r.reason, ''), r.received_at
		FROM email_receipts r LEFT JOIN email_inboxes i ON i.id = r.inbox_id LEFT JOIN projects p ON p.id = i.project_id
		WHERE r.intake_id = ? AND r.outcome <> 'processing' ORDER BY r.received_at DESC, r.rowid DESC LIMIT ?`, intakeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	receipts := []EmailReceipt{}
	for rows.Next() {
		var receipt EmailReceipt
		if err := rows.Scan(&receipt.ID, &receipt.InboxID, &receipt.InboxName, &receipt.Sender, &receipt.Subject, &receipt.Outcome, &receipt.TaskKey, &receipt.ProjectSlug, &receipt.OccurrenceCount, &receipt.Reason, &receipt.ReceivedAt); err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}

// claimEmailReceiptTx records a receipt before its alert is ingested. When
// the message was already processed it returns the earlier outcome instead.
func claimEmailReceiptTx(ctx context.Context, tx *sql.Tx, receipt IntakeReceipt, inboxID, received string) (string, *AlertDisposition, error) {
	id := newID()
	result, err := tx.ExecContext(ctx, `INSERT INTO email_receipts(id, intake_id, receipt_sha256, inbox_id, sender, subject, outcome, received_at) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, 'processing', ?) ON CONFLICT(intake_id, receipt_sha256) DO NOTHING`,
		id, receipt.IntakeID, secretDigest(receipt.Key), inboxID, receipt.Sender, receipt.Subject, received)
	if err != nil {
		return "", nil, err
	}
	if count, _ := result.RowsAffected(); count == 1 {
		return id, nil, pruneEmailReceiptsTx(ctx, tx, receipt.IntakeID)
	}
	var prior AlertDisposition
	var taskID, taskKey sql.NullString
	var outcome string
	if err := tx.QueryRowContext(ctx, `SELECT outcome, task_id, task_key, occurrence_count FROM email_receipts WHERE intake_id = ? AND receipt_sha256 = ?`, receipt.IntakeID, secretDigest(receipt.Key)).Scan(&outcome, &taskID, &taskKey, &prior.OccurrenceCount); err != nil {
		return "", nil, err
	}
	prior.Disposition, prior.TaskID, prior.TaskKey = "duplicate", taskID.String, taskKey.String
	return "", &prior, nil
}

func completeEmailReceiptTx(ctx context.Context, tx *sql.Tx, id string, result AlertDisposition) error {
	_, err := tx.ExecContext(ctx, `UPDATE email_receipts SET outcome = ?, task_id = NULLIF(?, ''), task_key = NULLIF(?, ''), occurrence_count = ? WHERE id = ?`, result.Disposition, result.TaskID, result.TaskKey, result.OccurrenceCount, id)
	return err
}
