package db

import (
	"context"
	"testing"
)

func TestRequestActivityMigrationPreservesPopulatedDatabaseAndRetainedWrites(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 25 {
		t.Skip("migration 025 is not present")
	}
	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, 24)
	populateProductionFixture(t, ctx, database, 24)
	var beforeTasks int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&beforeTasks); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate populated pre-025 database: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO request_activity_hourly(hour,actor_id,actor_kind,requests,errors,duration_ms_sum,last_seen_at) VALUES('2026-01-02T03:00:00Z','agent','agent',5,1,40,'2026-01-02T03:10:00Z')`); err != nil {
		t.Fatalf("insert migrated request activity: %v", err)
	}
	// A retained pre-025 binary ignores the new table and keeps writing tasks.
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET title='Retained write',version=version+1 WHERE id='task-1'`); err != nil {
		t.Fatalf("retained task write: %v", err)
	}
	var afterTasks, rows int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&afterTasks); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_activity_hourly`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if beforeTasks != afterTasks || rows != 1 {
		t.Fatalf("migration counts tasks=%d/%d request_rows=%d", beforeTasks, afterTasks, rows)
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("migration 025 integrity: %v", err)
	}
}
