package store

import (
	"context"
	"database/sql"
	"time"
)

type AttachmentRetentionRecord struct {
	ID, SHA256, Status, CreatedAt string
	SizeBytes                     int64
	Ready                         bool
	DeletedAt                     *string
}

func (s *Store) AttachmentRetentionRecords(ctx context.Context) ([]AttachmentRetentionRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,sha256,status,created_at,size_bytes,ready,deleted_at FROM task_attachments`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AttachmentRetentionRecord{}
	for rows.Next() {
		var a AttachmentRetentionRecord
		if err := rows.Scan(&a.ID, &a.SHA256, &a.Status, &a.CreatedAt, &a.SizeBytes, &a.Ready, &a.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) SetAttachmentReadiness(ctx context.Context, id string, ready bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE task_attachments SET ready=? WHERE id=?`, ready, id)
	return err
}

// PurgeAttachmentWithBlob rechecks expiry while holding the database writer
// lock. A concurrent publication must never lose its newly published original.
func (s *Store) PurgeAttachmentWithBlob(ctx context.Context, id string, at time.Time, remove func() error) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE task_attachments SET id=id WHERE id=?`, id); err != nil {
			return err
		}
		var expired bool
		if err := tx.QueryRowContext(ctx, `SELECT ((status='staged' AND created_at<?) OR (status='deleted' AND deleted_at<?)) FROM task_attachments WHERE id=?`, at.Add(-24*time.Hour).Format(time.RFC3339Nano), at.Add(-30*24*time.Hour).Format(time.RFC3339Nano), id).Scan(&expired); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
		if !expired {
			return nil
		}
		if err := remove(); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM task_attachments WHERE id=?`, id)
		return err
	})
}
