package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

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
			(a.kind='agent' AND (
   NOT EXISTS (SELECT 1 FROM actor_projects ceiling WHERE ceiling.actor_id=a.id)
   OR EXISTS (SELECT 1 FROM actor_projects allowed WHERE allowed.actor_id=a.id AND allowed.project_id=?)))
 OR (a.kind='human' AND EXISTS (SELECT 1 FROM workspace_memberships wm WHERE wm.actor_id=a.id
   AND (wm.role IN ('owner','admin') OR EXISTS (SELECT 1 FROM project_memberships pm WHERE pm.actor_id=a.id AND pm.project_id=?))))
		  )
	)`, actorID, projectID, projectID).Scan(&eligible)
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
