package db

import (
	"context"
	"testing"
)

func TestAgentNotesMigrationPreservesPopulatedDatabaseAndRetainedWrites(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 22 {
		t.Skip("migration 022 is not present")
	}
	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, 21)
	populateProductionFixture(t, ctx, database, 21)
	var beforeTasks, beforeComments int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&beforeTasks); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments`).Scan(&beforeComments); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate populated pre-022 database: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO agent_notes(id,task_id,actor_id,category,body,evidence_json,created_at,updated_at) VALUES('note-1','task-1','agent','known_issue','Verified issue','["internal/store"]','2026-01-02T00:00:00Z','2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert migrated agent note: %v", err)
	}
	// A retained pre-022 binary can continue mutating the old task shape.
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET title='Retained write',version=version+1 WHERE id='task-1'`); err != nil {
		t.Fatalf("retained task write: %v", err)
	}
	var afterTasks, afterComments, notes int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&afterTasks); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments`).Scan(&afterComments); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_notes`).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if beforeTasks != afterTasks || beforeComments != afterComments || notes != 1 {
		t.Fatalf("migration counts tasks=%d/%d comments=%d/%d notes=%d", beforeTasks, afterTasks, beforeComments, afterComments, notes)
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("migration 022 integrity: %v", err)
	}
}
