package store

import (
	"context"
	"database/sql"
	"errors"
)

// linkTaskAttachmentsTx participates in the caller's publication transaction.
// IDs refer to immutable publication history, never the replaceable latest pulse.
func linkTaskAttachmentsTx(ctx context.Context, tx *sql.Tx, taskID, actorID string, ids []string, commentID, progressID string, allowAdmin bool) ([]Attachment, error) {
	if len(ids) > 4 {
		return nil, &Error{Kind: ErrInvalid, Message: "at most four images may be published per update"}
	}
	out := []Attachment{}
	seen := map[string]bool{}
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM tasks WHERE id=? AND deleted_at IS NULL`, taskID).Scan(&projectID); err != nil {
		return nil, err
	}
	for position, id := range ids {
		if seen[id] {
			return nil, &Error{Kind: ErrInvalid, Message: "duplicate attachment ID"}
		}
		seen[id] = true
		a, err := attachmentFromRow(tx.QueryRowContext(ctx, `SELECT `+attachmentColumns+` FROM task_attachments a WHERE a.id=? AND a.ready=1`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &Error{Kind: ErrInvalid, Message: "attachment is unavailable"}
		}
		if err != nil {
			return nil, err
		}
		if a.TaskID != taskID || a.ProjectID != projectID || a.DeletedAt != nil || a.Status != "staged" || (a.ActorID != actorID && !allowAdmin) {
			return nil, &Error{Kind: ErrInvalid, Message: "attachment must be unpublished and owned by this author on this task"}
		}
		if (commentID == "") == (progressID == "") {
			return nil, &Error{Kind: ErrInvalid, Message: "attachment publication source is required"}
		}
		stamp := now()
		if _, err = tx.ExecContext(ctx, `INSERT INTO task_attachment_links(attachment_id,comment_id,progress_id,created_at,position) VALUES(?,NULLIF(?,''),NULLIF(?,''),?,?)`, id, commentID, progressID, stamp, position); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE task_attachments SET status='published',version=version+1,updated_at=? WHERE id=?`, stamp, id); err != nil {
			return nil, err
		}
		a.Status = "published"
		a.Version++
		a.UpdatedAt = stamp
		out = append(out, a)
	}
	return out, nil
}
func (s *Store) AttachmentsForComment(ctx context.Context, id string) ([]Attachment, error) {
	return s.attachmentsForSource(ctx, "comment_id", id)
}
func (s *Store) AttachmentsForProgress(ctx context.Context, id string) ([]Attachment, error) {
	return s.attachmentsForSource(ctx, "progress_id", id)
}
func (s *Store) attachmentsForSource(ctx context.Context, column, id string) ([]Attachment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+attachmentColumns+` FROM task_attachments a JOIN task_attachment_links l ON l.attachment_id=a.id WHERE l.`+column+`=? AND a.status='published' AND `+visibleAttachment+` ORDER BY l.position,a.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		a, err := attachmentFromRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
