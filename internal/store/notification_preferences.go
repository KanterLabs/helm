package store

import (
	"context"
	"database/sql"
	"errors"
)

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
