package store

import (
	"context"
	"database/sql"
	"strings"
)

// activityAttachmentScanner extends the shared attachment scanner with the
// activity anchor columns selected before the public attachment fields.
type activityAttachmentScanner struct {
	rows                  *sql.Rows
	commentID, progressID *sql.NullString
}

func (s activityAttachmentScanner) Scan(dest ...any) error {
	return s.rows.Scan(append([]any{s.commentID, s.progressID}, dest...)...)
}

// activityAttachments reads one bounded page's image groups in one query.
// Both the generated progress comment and its immutable history row resolve
// to the same link; no duplicate links or mutable snapshot anchors are used.
func (s *Store) activityAttachments(ctx context.Context, commentIDs, progressIDs []string) (map[string][]Attachment, map[string][]Attachment, error) {
	comments := make(map[string][]Attachment)
	progress := make(map[string][]Attachment)
	if len(commentIDs)+len(progressIDs) == 0 {
		return comments, progress, nil
	}
	clauses := make([]string, 0, 2)
	args := make([]any, 0, len(commentIDs)+len(progressIDs))
	if len(commentIDs) > 0 {
		clauses = append(clauses, "COALESCE(l.comment_id,h.generated_comment_id) IN ("+strings.TrimSuffix(strings.Repeat("?,", len(commentIDs)), ",")+")")
		for _, id := range commentIDs {
			args = append(args, id)
		}
	}
	if len(progressIDs) > 0 {
		clauses = append(clauses, "l.progress_id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(progressIDs)), ",")+")")
		for _, id := range progressIDs {
			args = append(args, id)
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT COALESCE(l.comment_id,h.generated_comment_id), l.progress_id, `+attachmentColumns+`
		FROM task_attachment_links l JOIN task_attachments a ON a.id=l.attachment_id
		LEFT JOIN task_agent_work_history h ON h.id=l.progress_id
		LEFT JOIN comments c ON c.id=COALESCE(l.comment_id,h.generated_comment_id)
		WHERE a.status='published' AND a.ready=1 AND a.deleted_at IS NULL
		AND (c.id IS NULL OR c.deleted_at IS NULL) AND (`+strings.Join(clauses, " OR ")+`)
		ORDER BY l.position, a.id`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var commentID, progressID sql.NullString
		attachment, err := attachmentFromRow(activityAttachmentScanner{rows, &commentID, &progressID})
		if err != nil {
			return nil, nil, err
		}
		if commentID.Valid {
			comments[commentID.String] = append(comments[commentID.String], attachment)
		}
		if progressID.Valid {
			progress[progressID.String] = append(progress[progressID.String], attachment)
		}
	}
	return comments, progress, rows.Err()
}

func (s *Store) hydrateCommentAttachments(ctx context.Context, comments []Comment) error {
	ids := make([]string, 0, len(comments))
	for _, comment := range comments {
		ids = append(ids, comment.ID)
	}
	groups, _, err := s.activityAttachments(ctx, ids, nil)
	if err != nil {
		return err
	}
	for i := range comments {
		comments[i].Attachments = groups[comments[i].ID]
	}
	return nil
}

func (s *Store) hydrateTimelineAttachments(ctx context.Context, items []TaskTimelineItem) error {
	var commentIDs, progressIDs []string
	for _, item := range items {
		if item.Comment != nil {
			commentIDs = append(commentIDs, item.ID)
		}
		if item.Progress != nil {
			progressIDs = append(progressIDs, item.ID)
		}
	}
	comments, progress, err := s.activityAttachments(ctx, commentIDs, progressIDs)
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].Comment != nil {
			items[i].Comment.Attachments = comments[items[i].ID]
		}
		if items[i].Progress != nil {
			items[i].Progress.Attachments = progress[items[i].ID]
		}
	}
	return nil
}
