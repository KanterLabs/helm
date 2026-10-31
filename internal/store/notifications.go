package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// Notification fan-out is intentionally bounded. A single event cannot
// create an unbounded inbox write storm.
const MaxNotificationFanout = 100

func watchFromRow(scanner interface{ Scan(...any) error }) (Watch, error) {
	var watch Watch
	var taskID sql.NullString
	if err := scanner.Scan(&watch.ID, &watch.ActorID, &watch.ProjectID, &taskID, &watch.CreatedAt); err != nil {
		return Watch{}, err
	}
	watch.TaskID = nullableString(taskID)
	return watch, nil
}

func notificationFromRow(scanner interface{ Scan(...any) error }) (Notification, error) {
	var notification Notification
	var actorID, projectID, taskID, payload, readAt sql.NullString
	if err := scanner.Scan(&notification.ID, &notification.RecipientID, &actorID, &notification.EventType, &projectID, &taskID, &notification.Title, &notification.Body, &payload, &notification.DedupeKey, &readAt, &notification.CreatedAt, &notification.UpdatedAt); err != nil {
		return Notification{}, err
	}
	notification.ActorID = nullableString(actorID)
	notification.ProjectID = nullableString(projectID)
	notification.TaskID = nullableString(taskID)
	notification.Type = notification.EventType
	notification.ReadAt = nullableString(readAt)
	if !payload.Valid || strings.TrimSpace(payload.String) == "" {
		notification.Payload = json.RawMessage(`{}`)
	} else {
		notification.Payload = json.RawMessage(payload.String)
	}
	return notification, nil
}

func notificationSelect() string {
	return `SELECT id, recipient_id, actor_id, event_type, project_id, task_id, title, body, payload, dedupe_key, read_at, created_at, updated_at FROM notifications`
}

func (s *Store) ListNotifications(ctx context.Context, actorID string, filter NotificationFilter) ([]Notification, bool, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := notificationSelect() + ` WHERE recipient_id=?`
	args := []any{actorID}
	query, args = appendNotificationVisibility(query, args, actorID, filter.ProjectIDs, "notifications.project_id")
	if filter.UnreadOnly {
		query += ` AND read_at IS NULL`
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, filter.Limit+1, filter.Offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]Notification, 0, filter.Limit)
	for rows.Next() {
		item, err := notificationFromRow(rows)
		if err != nil {
			return nil, false, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(result) > filter.Limit
	if hasMore {
		result = result[:filter.Limit]
	}
	return result, hasMore, nil
}

func (s *Store) MarkNotificationRead(ctx context.Context, actorID, notificationID string) (Notification, error) {
	return s.MarkNotificationReadScoped(ctx, actorID, notificationID, nil)
}

func (s *Store) MarkNotificationUnread(ctx context.Context, actorID, notificationID string) (Notification, error) {
	return s.MarkNotificationUnreadScoped(ctx, actorID, notificationID, nil)
}

func (s *Store) MarkNotificationReadScoped(ctx context.Context, actorID, notificationID string, projectIDs []string) (Notification, error) {
	return s.setNotificationRead(ctx, actorID, notificationID, true, projectIDs)
}

func (s *Store) MarkNotificationUnreadScoped(ctx context.Context, actorID, notificationID string, projectIDs []string) (Notification, error) {
	return s.setNotificationRead(ctx, actorID, notificationID, false, projectIDs)
}

// SetNotificationRead changes one owned inbox item in either direction.
func (s *Store) SetNotificationRead(ctx context.Context, actorID, notificationID string, read bool) (Notification, error) {
	return s.SetNotificationReadScoped(ctx, actorID, notificationID, read, nil)
}

// SetNotificationReadScoped changes one owned inbox item, honoring the
// effective bearer project allow-list as well as the actor ceiling.
func (s *Store) SetNotificationReadScoped(ctx context.Context, actorID, notificationID string, read bool, projectIDs []string) (Notification, error) {
	return s.setNotificationRead(ctx, actorID, notificationID, read, projectIDs)
}

func (s *Store) setNotificationRead(ctx context.Context, actorID, notificationID string, read bool, projectIDs []string) (Notification, error) {
	updatedAt := now()
	var readAt any
	if read {
		readAt = updatedAt
	}
	query := `UPDATE notifications SET read_at=?, updated_at=? WHERE id=? AND recipient_id=?`
	args := []any{readAt, updatedAt, notificationID, actorID}
	query, args = appendNotificationVisibility(query, args, actorID, projectIDs, "notifications.project_id")
	result, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return Notification{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return Notification{}, notFound("notification not found")
	}
	query = notificationSelect() + ` WHERE id=? AND recipient_id=?`
	args = []any{notificationID, actorID}
	query, args = appendNotificationVisibility(query, args, actorID, projectIDs, "notifications.project_id")
	row := s.DB.QueryRowContext(ctx, query, args...)
	return notificationFromRow(row)
}

func (s *Store) MarkNotificationsRead(ctx context.Context, actorID string, notificationIDs []string) (int, error) {
	return s.MarkNotificationsReadScoped(ctx, actorID, notificationIDs, nil)
}

func (s *Store) MarkNotificationsReadScoped(ctx context.Context, actorID string, notificationIDs []string, projectIDs []string) (int, error) {
	if len(notificationIDs) == 0 {
		return 0, nil
	}
	ids := uniqueStrings(notificationIDs)
	if len(ids) > 200 {
		return 0, invalid("at most 200 notifications may be marked at once", nil)
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	readAt := now()
	args = append(args, readAt, readAt, actorID)
	query := `UPDATE notifications SET read_at=COALESCE(read_at, ?), updated_at=? WHERE id IN (` + strings.Join(placeholders, ",") + `) AND recipient_id=?`
	query, args = appendNotificationVisibility(query, args, actorID, projectIDs, "notifications.project_id")
	result, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

func (s *Store) MarkAllNotificationsRead(ctx context.Context, actorID string) (int, error) {
	return s.MarkAllNotificationsReadScoped(ctx, actorID, nil)
}

func (s *Store) MarkAllNotificationsReadScoped(ctx context.Context, actorID string, projectIDs []string) (int, error) {
	readAt := now()
	query := `UPDATE notifications SET read_at=COALESCE(read_at, ?), updated_at=? WHERE recipient_id=? AND read_at IS NULL`
	args := []any{readAt, readAt, actorID}
	query, args = appendNotificationVisibility(query, args, actorID, projectIDs, "notifications.project_id")
	result, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

func (s *Store) UnreadNotificationCount(ctx context.Context, actorID string) (int, error) {
	return s.UnreadNotificationCountScoped(ctx, actorID, nil)
}

func (s *Store) UnreadNotificationCountScoped(ctx context.Context, actorID string, projectIDs []string) (int, error) {
	query := `SELECT COUNT(1) FROM notifications WHERE recipient_id=? AND read_at IS NULL`
	args := []any{actorID}
	query, args = appendNotificationVisibility(query, args, actorID, projectIDs, "notifications.project_id")
	var count int
	err := s.DB.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}
