package db

import (
	"context"
	"testing"

	_ "modernc.org/sqlite"
)

// TestNotificationsMigrationIsAdditiveAndRetainedWritesSurvive exercises the
// populated 020 -> 021 upgrade boundary. The fixture deliberately contains
// existing board data before migration 021; the notification read model is
// then populated and a retained-binary task write is issued without knowing
// about the new tables.
func TestNotificationsMigrationIsAdditiveAndRetainedWritesSurvive(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 21 {
		t.Skip("migration 021 is not present")
	}
	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, 20)
	populateProductionFixture(t, ctx, database, 20)

	var beforeTitle string
	if err := database.QueryRowContext(ctx, `SELECT title FROM tasks WHERE id='task-1'`).Scan(&beforeTitle); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate populated pre-021 database: %v", err)
	}

	for _, table := range []string{"watches", "notification_preferences", "notifications"} {
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("migration 021 table %q count = %d, want 1", table, count)
		}
	}
	for _, table := range []string{"notification_deliveries", "automation_rules", "automation_runs"} {
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("deferred TC-165/TC-166 table %q unexpectedly created", table)
		}
	}

	// Populate every notification relation using existing actors/projects/tasks,
	// proving the new foreign keys and JSON checks accept a normal upgrade.
	if _, err := database.ExecContext(ctx, `INSERT INTO watches(id, actor_id, project_id, task_id, created_at) VALUES ('watch-1', 'agent', 'project', 'task-1', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert migrated watch: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO notification_preferences(actor_id, updated_at) VALUES ('agent', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert migrated notification preferences: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO notifications(id, recipient_id, actor_id, event_type, project_id, task_id, title, body, payload, dedupe_key, created_at, updated_at) VALUES ('notification-1', 'agent', 'owner', 'task.updated', 'project', 'task-1', 'Updated', 'Retained row', '{"version":3}', 'event-1', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert migrated notification: %v", err)
	}

	// This SQL is intentionally limited to the pre-021 task shape. Its success
	// and the preserved notification row demonstrate rollback compatibility.
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET title='Retained notification write', version=version+1, updated_at='2026-01-03T00:00:00Z' WHERE id='task-1' AND version=3`); err != nil {
		t.Fatalf("retained task write after 021: %v", err)
	}
	var title string
	if err := database.QueryRowContext(ctx, `SELECT title FROM tasks WHERE id='task-1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title == beforeTitle || title != "Retained notification write" {
		t.Fatalf("retained task title = %q, want retained write", title)
	}
	var notificationCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE id='notification-1'`).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 1 {
		t.Fatalf("notification row after retained write = %d, want 1", notificationCount)
	}
	var schemaVersion int
	if err := database.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != 21 {
		t.Fatalf("schema version after migration = %d, want 21", schemaVersion)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("rerun migration 021: %v", err)
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("migration 021 integrity: %v", err)
	}
}
