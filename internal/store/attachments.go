package store

import (
	"context"
	"database/sql"
	"errors"
)

type Attachment struct {
	ID             string  `json:"id"`
	TaskID         string  `json:"task_id"`
	ProjectID      string  `json:"project_id"`
	ActorID        string  `json:"actor_id"`
	Filename       string  `json:"filename"`
	ContentType    string  `json:"content_type"`
	SizeBytes      int64   `json:"size_bytes"`
	StoredBytes    int64   `json:"-"`
	SHA256         string  `json:"sha256"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	Status         string  `json:"status"`
	Caption        string  `json:"caption"`
	CommitSHA      string  `json:"commit_sha"`
	Build          string  `json:"build"`
	ViewportWidth  int     `json:"viewport_width"`
	ViewportHeight int     `json:"viewport_height"`
	Version        int64   `json:"version"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	DeletedAt      *string `json:"deleted_at,omitempty"`
}

const attachmentColumns = `a.id,a.task_id,a.project_id,a.actor_id,a.filename,a.content_type,a.size_bytes,a.sha256,a.width,a.height,a.status,a.caption,a.commit_sha,a.build,a.viewport_width,a.viewport_height,a.version,a.created_at,a.updated_at,a.deleted_at`

func attachmentFromRow(row interface{ Scan(...any) error }) (Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.TaskID, &a.ProjectID, &a.ActorID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.Width, &a.Height, &a.Status, &a.Caption, &a.CommitSHA, &a.Build, &a.ViewportWidth, &a.ViewportHeight, &a.Version, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt)
	return a, err
}

const visibleAttachment = `a.ready=1 AND a.status<>'deleted' AND a.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM task_attachment_links l JOIN comments c ON c.id=l.comment_id WHERE l.attachment_id=a.id AND c.deleted_at IS NOT NULL) AND NOT EXISTS(SELECT 1 FROM task_attachment_links l JOIN task_agent_work_history h ON h.id=l.progress_id JOIN comments c ON c.id=h.generated_comment_id WHERE l.attachment_id=a.id AND c.deleted_at IS NOT NULL)`

func (s *Store) GetAttachment(ctx context.Context, id string) (Attachment, error) {
	a, err := attachmentFromRow(s.DB.QueryRowContext(ctx, `SELECT `+attachmentColumns+` FROM task_attachments a WHERE a.id=? AND `+visibleAttachment, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}
func (s *Store) ListAttachments(ctx context.Context, taskID, actorID string, staged, admin bool, limit, offset int) ([]Attachment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+attachmentColumns+` FROM task_attachments a WHERE a.task_id=? AND `+visibleAttachment+` AND (a.status='published' OR (? AND a.status='staged' AND (a.actor_id=? OR ?))) ORDER BY a.created_at DESC,a.id DESC LIMIT ? OFFSET ?`, taskID, staged, actorID, admin, limit, offset)
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

// ReserveAttachment atomically accounts retained bytes before installation.
// Only MarkAttachmentReady makes the reserved object visible to readers.
func (s *Store) ReserveAttachment(ctx context.Context, a Attachment, key, hash string, quota int64) (Attachment, bool, error) {
	if a.StoredBytes < a.SizeBytes {
		a.StoredBytes = a.SizeBytes
	}
	replay := false
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if key != "" {
			existing, e := attachmentFromRow(tx.QueryRowContext(ctx, `SELECT `+attachmentColumns+` FROM task_attachments a WHERE a.actor_id=? AND a.task_id=? AND a.idempotency_key=?`, a.ActorID, a.TaskID, key))
			if e == nil {
				var oldHash string
				var ready int
				if e = tx.QueryRowContext(ctx, `SELECT request_hash,ready FROM task_attachments WHERE id=?`, existing.ID).Scan(&oldHash, &ready); e != nil {
					return e
				}
				if oldHash != hash {
					return &Error{Kind: ErrConflict, Message: "Idempotency-Key was used for a different upload"}
				}
				if ready != 1 || existing.Status == "deleted" {
					return &Error{Kind: ErrConflict, Message: "upload is unavailable; use a new Idempotency-Key"}
				}
				a = existing
				replay = true
				return nil
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		// SQLite serializes this write before the quota read, including on deferred tx.
		if _, e := tx.ExecContext(ctx, `UPDATE projects SET id=id WHERE id=?`, a.ProjectID); e != nil {
			return e
		}
		var used int64
		if e := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN stored_bytes>0 THEN stored_bytes ELSE size_bytes END),0) FROM task_attachments WHERE project_id=?`, a.ProjectID).Scan(&used); e != nil {
			return e
		}
		if used > quota-a.StoredBytes {
			return &Error{Kind: ErrConflict, Message: "project attachment storage quota exceeded"}
		}
		a.ID = newID()
		a.Version = 1
		a.Status = "staged"
		a.CreatedAt = now()
		a.UpdatedAt = a.CreatedAt
		_, e := tx.ExecContext(ctx, `INSERT INTO task_attachments(id,task_id,project_id,actor_id,filename,content_type,size_bytes,sha256,width,height,status,caption,commit_sha,build,viewport_width,viewport_height,version,created_at,updated_at,idempotency_key,request_hash,stored_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULLIF(?,''),?,?)`, a.ID, a.TaskID, a.ProjectID, a.ActorID, a.Filename, a.ContentType, a.SizeBytes, a.SHA256, a.Width, a.Height, a.Status, a.Caption, a.CommitSHA, a.Build, a.ViewportWidth, a.ViewportHeight, a.Version, a.CreatedAt, a.UpdatedAt, key, hash, a.StoredBytes)
		return e
	})
	return a, replay, err
}
func (s *Store) MarkAttachmentReady(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE task_attachments SET ready=1 WHERE id=? AND status='staged' AND ready=0`, id)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed == 0 {
			return err
		}
		var actorID, projectID, taskID string
		if err = tx.QueryRowContext(ctx, `SELECT actor_id,project_id,task_id FROM task_attachments WHERE id=?`, id).Scan(&actorID, &projectID, &taskID); err != nil {
			return err
		}
		_, err = insertEvent(ctx, tx, "attachment.uploaded", actorID, projectID, taskID, map[string]any{"attachment_count": 1})
		return err
	})
}
func (s *Store) CancelAttachmentReservation(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM task_attachments WHERE id=? AND ready=0`, id)
	return err
}
func (s *Store) UpdateAttachment(ctx context.Context, id, actorID string, version int64, admin, remove bool, caption, commit, build string, vw, vh int) (Attachment, error) {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		a, e := attachmentFromRow(tx.QueryRowContext(ctx, `SELECT `+attachmentColumns+` FROM task_attachments a WHERE id=? AND `+visibleAttachment, id))
		if errors.Is(e, sql.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if a.ActorID != actorID && !admin {
			return &Error{Kind: ErrForbidden, Message: "only the attachment creator can edit it"}
		}
		if a.Version != version {
			return &Error{Kind: ErrConflict, Message: "attachment version has changed"}
		}
		status := a.Status
		var deleted any
		if remove {
			status = "deleted"
			deleted = now()
		}
		result, e := tx.ExecContext(ctx, `UPDATE task_attachments SET caption=?,commit_sha=?,build=?,viewport_width=?,viewport_height=?,status=?,deleted_at=?,version=version+1,updated_at=? WHERE id=? AND version=?`, caption, commit, build, vw, vh, status, deleted, now(), id, version)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		kind := "attachment.updated"
		if remove {
			kind = "attachment.deleted"
		}
		_, e = insertEvent(ctx, tx, kind, actorID, a.ProjectID, a.TaskID, map[string]any{"attachment_count": 1, "version": version + 1})
		return e
	})
	if err != nil {
		return Attachment{}, err
	}
	if remove {
		return Attachment{}, nil
	}
	return s.GetAttachment(ctx, id)
}
