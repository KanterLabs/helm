package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
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

func (s *Store) CreateWatch(ctx context.Context, actorID, projectReference, taskReference string) (Watch, error) {
	if strings.TrimSpace(actorID) == "" {
		return Watch{}, invalid("actor is required", nil)
	}
	if _, err := s.GetActor(ctx, actorID); err != nil {
		return Watch{}, err
	}
	project, err := s.GetProject(ctx, projectReference)
	if err != nil {
		return Watch{}, err
	}
	taskID := ""
	if strings.TrimSpace(taskReference) != "" {
		task, err := s.ResolveTaskReference(ctx, taskReference)
		if err != nil {
			return Watch{}, err
		}
		if task.ProjectID != project.ID {
			return Watch{}, invalid("task belongs to another project", nil)
		}
		taskID = task.ID
	}
	id, created := newID(), now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO watches(id, actor_id, project_id, task_id, created_at) VALUES (?, ?, ?, NULLIF(?, ''), ?)`, id, actorID, project.ID, taskID, created); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return &Error{Kind: ErrAlreadyExists, Message: "watch already exists"}
			}
			if strings.Contains(strings.ToLower(err.Error()), "watch_task_project_mismatch") {
				return invalid("task belongs to another project", nil)
			}
			return err
		}
		watchType := "project"
		if taskID != "" {
			watchType = "task"
		}
		_, err := insertEvent(ctx, tx, "watch.created", actorID, project.ID, taskID, map[string]any{"watch_id": id, "watch_type": watchType})
		return err
	})
	if err != nil {
		return Watch{}, err
	}
	return s.GetWatch(ctx, id, actorID)
}

func (s *Store) CreateProjectWatch(ctx context.Context, actorID, projectReference string) (Watch, error) {
	return s.CreateWatch(ctx, actorID, projectReference, "")
}

func (s *Store) CreateTaskWatch(ctx context.Context, actorID, taskReference string) (Watch, error) {
	task, err := s.ResolveTaskReference(ctx, taskReference)
	if err != nil {
		return Watch{}, err
	}
	return s.CreateWatch(ctx, actorID, task.ProjectID, task.ID)
}

func (s *Store) GetWatch(ctx context.Context, id, actorID string) (Watch, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, actor_id, project_id, task_id, created_at FROM watches WHERE id=?`, id)
	watch, err := watchFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Watch{}, notFound("watch not found")
	}
	if err != nil {
		return Watch{}, err
	}
	if actorID != "" && watch.ActorID != actorID {
		return Watch{}, forbidden("watch belongs to another actor")
	}
	return watch, nil
}

// appendNotificationVisibility applies both the durable actor project ceiling
// and an optional bearer-token allow-list. A nil projectIDs value means the
// caller is unscoped; an empty, non-nil value intentionally matches no rows.
func appendNotificationVisibility(query string, args []any, actorID string, projectIDs []string, projectColumn string) (string, []any) {
	query += ` AND (
		NOT EXISTS (SELECT 1 FROM actor_projects ceiling WHERE ceiling.actor_id=?)
		OR EXISTS (SELECT 1 FROM actor_projects allowed WHERE allowed.actor_id=? AND allowed.project_id=` + projectColumn + `)
	)`
	args = append(args, actorID, actorID)
	if projectIDs == nil {
		return query, args
	}
	if len(projectIDs) == 0 {
		return query + ` AND 1=0`, args
	}
	placeholders := make([]string, len(projectIDs))
	for i, projectID := range projectIDs {
		placeholders[i] = `?`
		args = append(args, projectID)
	}
	query += ` AND ` + projectColumn + ` IN (` + strings.Join(placeholders, `,`) + `)`
	return query, args
}

// ListWatches returns watches owned by actorID. A project or task filter may
// be supplied by callers that need to render current watch state. The legacy
// unscoped entry point still enforces the actor's durable project ceiling.
func (s *Store) ListWatches(ctx context.Context, actorID, projectID, taskID string) ([]Watch, error) {
	return s.ListWatchesScoped(ctx, actorID, projectID, taskID, nil)
}

// ListWatchesScoped applies the effective project allow-list of a bearer
// token in addition to the actor's durable project ceiling.
func (s *Store) ListWatchesScoped(ctx context.Context, actorID, projectID, taskID string, projectIDs []string) ([]Watch, error) {
	query := `SELECT w.id, w.actor_id, w.project_id, w.task_id, w.created_at FROM watches w WHERE w.actor_id=?`
	args := []any{actorID}
	query, args = appendNotificationVisibility(query, args, actorID, projectIDs, "w.project_id")
	if projectID != "" {
		query += ` AND w.project_id=?`
		args = append(args, projectID)
	}
	if taskID != "" {
		query += ` AND (w.task_id IS NULL OR w.task_id=?)`
		args = append(args, taskID)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Watch, 0)
	for rows.Next() {
		watch, err := watchFromRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, watch)
	}
	return result, rows.Err()
}

func (s *Store) DeleteWatch(ctx context.Context, actorID, id string) error {
	var watch Watch
	row := s.DB.QueryRowContext(ctx, `SELECT id, actor_id, project_id, task_id, created_at FROM watches WHERE id=?`, id)
	var err error
	watch, err = watchFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound("watch not found")
	}
	if err != nil {
		return err
	}
	if watch.ActorID != actorID {
		return forbidden("watch belongs to another actor")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM watches WHERE id=? AND actor_id=?`, id, actorID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			return notFound("watch not found")
		}
		_, err = insertEvent(ctx, tx, "watch.deleted", actorID, watch.ProjectID, notificationStringValue(watch.TaskID), map[string]any{"watch_id": id})
		return err
	})
}

func notificationStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func defaultNotificationPreferences(actorID string) NotificationPreferences {
	return NotificationPreferences{ActorID: actorID, Assignments: true, Mentions: true, Blockers: true, StateChanges: true}
}

func notificationPreferencesFromRow(scanner interface{ Scan(...any) error }) (NotificationPreferences, error) {
	var preferences NotificationPreferences
	var assignments, mentions, blockers, stateChanges int
	if err := scanner.Scan(&preferences.ActorID, &assignments, &mentions, &blockers, &stateChanges, &preferences.UpdatedAt); err != nil {
		return NotificationPreferences{}, err
	}
	preferences.Assignments = boolValue(assignments)
	preferences.Mentions = boolValue(mentions)
	preferences.Blockers = boolValue(blockers)
	preferences.StateChanges = boolValue(stateChanges)
	return preferences, nil
}

func (s *Store) GetNotificationPreferences(ctx context.Context, actorID string) (NotificationPreferences, error) {
	if _, err := s.GetActor(ctx, actorID); err != nil {
		return NotificationPreferences{}, err
	}
	row := s.DB.QueryRowContext(ctx, `SELECT actor_id, assignments, mentions, blockers, state_changes, updated_at FROM notification_preferences WHERE actor_id=?`, actorID)
	preferences, err := notificationPreferencesFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultNotificationPreferences(actorID), nil
	}
	return preferences, err
}

func (s *Store) UpdateNotificationPreferences(ctx context.Context, actorID string, input NotificationPreferencesInput) (NotificationPreferences, error) {
	if _, err := s.GetActor(ctx, actorID); err != nil {
		return NotificationPreferences{}, err
	}
	current, err := s.GetNotificationPreferences(ctx, actorID)
	if err != nil {
		return NotificationPreferences{}, err
	}
	if input.Assignments != nil {
		current.Assignments = *input.Assignments
	}
	if input.Mentions != nil {
		current.Mentions = *input.Mentions
	}
	if input.Blockers != nil {
		current.Blockers = *input.Blockers
	}
	if input.StateChanges != nil {
		current.StateChanges = *input.StateChanges
	}
	updated := now()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO notification_preferences(actor_id, assignments, mentions, blockers, state_changes, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(actor_id) DO UPDATE SET assignments=excluded.assignments, mentions=excluded.mentions, blockers=excluded.blockers, state_changes=excluded.state_changes, updated_at=excluded.updated_at`, actorID, boolInt(current.Assignments), boolInt(current.Mentions), boolInt(current.Blockers), boolInt(current.StateChanges), updated)
	if err != nil {
		return NotificationPreferences{}, err
	}
	current.UpdatedAt = updated
	return current, nil
}

func notificationCategory(eventType string, payload map[string]any) string {
	switch eventType {
	case "comment.created":
		return "mentions"
	case "task.dependency_state_changed", "task.blocked":
		return "blockers"
	case "task.moved", "task.completed", "task.reopened", "bug.reopened":
		return "state_changes"
	case "task.updated", "task.created":
		if value, ok := payload["assignment_changed"].(bool); ok && value {
			return "assignments"
		}
	}
	return ""
}

func preferenceEnabled(preferences NotificationPreferences, category string) bool {
	switch category {
	case "assignments":
		return preferences.Assignments
	case "mentions":
		return preferences.Mentions
	case "blockers":
		return preferences.Blockers
	case "state_changes":
		return preferences.StateChanges
	default:
		return false
	}
}

func notificationPreferenceEnabledTx(ctx context.Context, tx dependencySQL, actorID, category string) (bool, error) {
	var assignments, mentions, blockers, stateChanges int
	err := tx.QueryRowContext(ctx, `SELECT assignments, mentions, blockers, state_changes FROM notification_preferences WHERE actor_id=?`, actorID).Scan(&assignments, &mentions, &blockers, &stateChanges)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return preferenceEnabled(NotificationPreferences{Assignments: boolValue(assignments), Mentions: boolValue(mentions), Blockers: boolValue(blockers), StateChanges: boolValue(stateChanges)}, category), nil
}

func notificationActorEligibleTx(ctx context.Context, tx dependencySQL, actorID, projectID string) (bool, error) {
	var eligible int
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1
		FROM actors a
		WHERE a.id=?
		  AND a.disabled_at IS NULL
		  AND (
			NOT EXISTS (SELECT 1 FROM actor_projects ceiling WHERE ceiling.actor_id=a.id)
			OR EXISTS (SELECT 1 FROM actor_projects allowed WHERE allowed.actor_id=a.id AND allowed.project_id=?)
		  )
	)`, actorID, projectID).Scan(&eligible)
	return eligible != 0, err
}

func notificationPayload(value string) map[string]any {
	var payload map[string]any
	if err := json.Unmarshal([]byte(value), &payload); err != nil || payload == nil {
		return map[string]any{}
	}
	return payload
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func notificationRecipientsTx(ctx context.Context, tx dependencySQL, projectID, taskID, directActor string, actorID string) ([]string, error) {
	// Direct recipients (assignment, mention, or claim owner) are sorted ahead
	// of watch fan-out so the bounded inbox never drops the primary audience.
	seen := map[string]struct{}{}
	result := make([]string, 0, MaxNotificationFanout)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || id == actorID {
			return
		}
		if _, exists := seen[id]; exists || len(result) >= MaxNotificationFanout {
			return
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	if directActor != "" {
		eligible, err := notificationActorEligibleTx(ctx, tx, directActor, projectID)
		if err != nil {
			return nil, err
		}
		if eligible {
			add(directActor)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT w.actor_id
		FROM watches w
		JOIN actors a ON a.id=w.actor_id
		WHERE w.project_id=?
		  AND (w.task_id IS NULL OR w.task_id=?)
		  AND a.disabled_at IS NULL
		  AND (
			NOT EXISTS (SELECT 1 FROM actor_projects ceiling WHERE ceiling.actor_id=w.actor_id)
			OR EXISTS (SELECT 1 FROM actor_projects allowed WHERE allowed.actor_id=w.actor_id AND allowed.project_id=w.project_id)
		  )
		ORDER BY w.actor_id
		LIMIT ?`, projectID, taskID, MaxNotificationFanout)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		add(id)
	}
	return result, rows.Err()
}

// isOptionalNotificationSchemaError lets retained binaries continue writing
// core events while a database is upgraded to the notification schema. The
// notification fan-out is best-effort until migration 021 is present.
func isOptionalNotificationSchemaError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, table := range []string{"notifications", "notification_preferences", "watches"} {
		if strings.Contains(message, "no such table: "+table) {
			return true
		}
	}
	return false
}

func notificationTextTx(ctx context.Context, tx dependencySQL, eventType, projectID, taskID string, payload map[string]any) (string, string) {
	title, body := "Board update", "A watched item changed."
	var taskTitle, taskKey string
	if taskID != "" {
		_ = tx.QueryRowContext(ctx, `SELECT p.key || '-' || CAST(t.number AS TEXT), t.title FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.id=?`, taskID).Scan(&taskKey, &taskTitle)
	}
	switch eventType {
	case "comment.created":
		title = "You were mentioned"
		body = "You were mentioned in " + taskKey
	case "task.dependency_state_changed":
		if satisfied, ok := payload["satisfied"].(bool); ok && satisfied {
			title = "Task is unblocked"
			body = taskKey + " is no longer blocked."
		} else {
			title = "Task is blocked"
			body = taskKey + " has an unmet prerequisite."
		}
	case "task.blocked":
		title = "Task was blocked"
		body = taskKey + " was moved to Blocked."
	case "task.moved", "task.completed", "task.reopened", "bug.reopened":
		title = "Task state changed"
		body = taskKey + " changed state."
	case "task.updated", "task.created":
		if payload["assignment_changed"] == true {
			title = "Task assigned to you"
			body = taskKey + " was assigned to you."
		} else {
			title = "Task updated"
			body = taskKey + " was updated."
		}
	}
	if taskTitle != "" && taskKey == "" {
		body = taskTitle
	}
	return title, body
}

func insertNotificationTx(ctx context.Context, tx dependencySQL, recipientID, actorID, eventType, projectID, taskID, title, body, dedupeKey string, payload any, createdAt string) error {
	if strings.TrimSpace(recipientID) == "" || strings.TrimSpace(dedupeKey) == "" {
		return nil
	}
	encoded := eventPayload(payload)
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO notifications(id, recipient_id, actor_id, event_type, project_id, task_id, title, body, payload, dedupe_key, created_at, updated_at) VALUES (?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?)`, newID(), recipientID, actorID, eventType, projectID, taskID, title, body, encoded, dedupeKey, createdAt, createdAt)
	return err
}

func notifyForEventTx(ctx context.Context, tx dependencySQL, eventID string, eventCursor int64, eventType, actorID, projectID, taskID, payloadJSON, createdAt string) error {
	// This query is intentionally guarded by the caller for retained binaries;
	// normal current databases always have the notification schema.
	payload := notificationPayload(payloadJSON)
	category := notificationCategory(eventType, payload)
	if eventType == "task.updated" {
		assignee := payloadString(payload, "assignee")
		previous := payloadString(payload, "previous_assignee")
		payload["assignment_changed"] = assignee != "" && assignee != previous
		category = notificationCategory(eventType, payload)
	}
	if eventType == "task.created" {
		var assignee sql.NullString
		if taskID != "" {
			if err := tx.QueryRowContext(ctx, `SELECT assignee_id FROM tasks WHERE id=?`, taskID).Scan(&assignee); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if assignee.Valid && assignee.String != "" {
			payload["assignment_changed"] = true
			payload["assignee"] = assignee.String
			category = "assignments"
		}
	}
	if eventType == "comment.created" {
		var body string
		if commentID := payloadString(payload, "comment_id"); commentID != "" {
			if err := tx.QueryRowContext(ctx, `SELECT body FROM comments WHERE id=?`, commentID).Scan(&body); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		mentioned, err := mentionedActorsTx(ctx, tx, projectID, body)
		if err != nil {
			return err
		}
		for _, recipient := range mentioned {
			enabled, err := notificationPreferenceEnabledTx(ctx, tx, recipient, "mentions")
			if err != nil {
				return err
			}
			if !enabled {
				continue
			}
			title, text := notificationTextTx(ctx, tx, eventType, projectID, taskID, payload)
			if err := insertNotificationTx(ctx, tx, recipient, actorID, eventType, projectID, taskID, title, text, "event:"+eventID+":mention:"+recipient, payload, createdAt); err != nil {
				return err
			}
		}
		// A mention event has no useful watch-only audience; explicit mentions
		// are the least surprising and least noisy behavior.
		return nil
	}
	if category == "" || projectID == "" {
		return nil
	}
	direct := ""
	if category == "assignments" {
		direct = payloadString(payload, "assignee")
	}
	recipients, err := notificationRecipientsTx(ctx, tx, projectID, taskID, direct, actorID)
	if err != nil {
		return err
	}
	title, text := notificationTextTx(ctx, tx, eventType, projectID, taskID, payload)
	for _, recipient := range recipients {
		enabled, err := notificationPreferenceEnabledTx(ctx, tx, recipient, category)
		if err != nil {
			return err
		}
		if !enabled {
			continue
		}
		if err := insertNotificationTx(ctx, tx, recipient, actorID, eventType, projectID, taskID, title, text, "event:"+eventID+":"+category, payload, createdAt); err != nil {
			return err
		}
	}
	_ = eventCursor
	return nil
}

func mentionedActorsTx(ctx context.Context, tx dependencySQL, projectID, body string) ([]string, error) {
	// Mentions accept @actor-id, @display-name, or @email local-part. The
	// parser is intentionally conservative: punctuation terminates a token and
	// results are deduplicated before the fan-out limit is applied.
	seen := map[string]struct{}{}
	result := make([]string, 0, MaxNotificationFanout)
	for _, raw := range strings.FieldsFunc(body, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' || r == ';' || r == ':' || r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}'
	}) {
		if !strings.HasPrefix(raw, "@") || len(raw) < 2 {
			continue
		}
		name := strings.Trim(raw[1:], ".!?<>\"'")
		if name == "" {
			continue
		}
		var actorID string
		err := tx.QueryRowContext(ctx, `SELECT a.id
			FROM actors a
			WHERE (a.id=? OR lower(a.name)=lower(?) OR lower(a.email)=lower(?) OR lower(substr(a.email, 1, instr(a.email, '@') - 1))=lower(?))
			  AND a.disabled_at IS NULL
			  AND (
				NOT EXISTS (SELECT 1 FROM actor_projects ceiling WHERE ceiling.actor_id=a.id)
				OR EXISTS (SELECT 1 FROM actor_projects allowed WHERE allowed.actor_id=a.id AND allowed.project_id=?)
			  )
			LIMIT 1`, name, name, name, name, projectID).Scan(&actorID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, exists := seen[actorID]; exists || len(result) >= MaxNotificationFanout {
			continue
		}
		seen[actorID] = struct{}{}
		result = append(result, actorID)
	}
	sort.Strings(result)
	return result, nil
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
