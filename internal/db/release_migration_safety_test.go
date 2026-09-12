package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestReleasesMigrationIsAdditiveAndGuardsRetainedWrites exercises the 021 ->
// 022 boundary with populated task data. Existing IDs and task fields remain
// unchanged, while the new nullable assignment and release lifecycle guards
// reject unsafe direct SQL writes.
func TestReleasesMigrationIsAdditiveAndGuardsRetainedWrites(t *testing.T) {
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

	var beforeID, beforeTitle string
	if err := database.QueryRowContext(ctx, `SELECT id, title FROM tasks WHERE id='task-1'`).Scan(&beforeID, &beforeTitle); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate populated pre-022 database: %v", err)
	}
	var tableCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='releases'`).Scan(&tableCount); err != nil || tableCount != 1 {
		t.Fatalf("releases table count = %d, %v", tableCount, err)
	}
	var releaseID sql.NullString
	if err := database.QueryRowContext(ctx, `SELECT release_id FROM tasks WHERE id='task-1'`).Scan(&releaseID); err != nil {
		t.Fatal(err)
	}
	if releaseID.Valid {
		t.Fatalf("existing task release_id = %q, want NULL", releaseID.String)
	}
	var afterID, afterTitle string
	if err := database.QueryRowContext(ctx, `SELECT id, title FROM tasks WHERE id='task-1'`).Scan(&afterID, &afterTitle); err != nil {
		t.Fatal(err)
	}
	if afterID != beforeID || afterTitle != beforeTitle {
		t.Fatalf("existing task changed during migration: before=%s/%s after=%s/%s", beforeID, beforeTitle, afterID, afterTitle)
	}

	if _, err := database.ExecContext(ctx, `INSERT INTO releases(id, project_id, name, description, version, created_at, updated_at) VALUES ('release-1', 'project', '1.4', '', 1, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert planned release: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET release_id='release-1' WHERE id='task-1'`); err != nil {
		t.Fatalf("assign planned release: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET release_id=NULL WHERE id='task-1'`); err != nil {
		t.Fatalf("clear planned release: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET release_id='release-1' WHERE id='task-1'`); err != nil {
		t.Fatalf("reassign planned release: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO projects(id, key, slug, name, created_at, updated_at) VALUES ('project-2', 'OTHER', 'other', 'Other', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert second project: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO releases(id, project_id, name, description, version, created_at, updated_at) VALUES ('release-2', 'project-2', '1.4', '', 1, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert second project release: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET release_id='release-2' WHERE id='task-1'`); err == nil || !containsSQLMessage(err, "release_cross_project_or_not_live") {
		t.Fatalf("cross-project assignment error = %v, want release_cross_project_or_not_live", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE releases SET released_at='2026-01-03T00:00:00Z', released_by='owner' WHERE id='release-1'`); err == nil || !containsSQLMessage(err, "release_incomplete") {
		t.Fatalf("incomplete release write error = %v, want release_incomplete", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO columns(id, project_id, name, semantic_state, position, created_at, updated_at) VALUES ('done-release', 'project', 'Done release', 'completed', 3, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert completed column: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET column_id='done-release', completed_at='2026-01-03T00:00:00Z' WHERE id='task-1'`); err != nil {
		t.Fatalf("complete release task: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE releases SET released_at='2026-01-03T00:00:00Z', released_by='owner' WHERE id='release-1'`); err != nil {
		t.Fatalf("complete release: %v", err)
	}
	// A retained pre-release binary can still edit an ordinary task field after
	// upgrade; that write must not clear the new release assignment.
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET title='Retained release write', version=version+1, updated_at='2026-01-03T00:01:00Z' WHERE id='task-1'`); err != nil {
		t.Fatalf("retained ordinary task write: %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT release_id FROM tasks WHERE id='task-1'`).Scan(&releaseID); err != nil {
		t.Fatal(err)
	}
	if !releaseID.Valid || releaseID.String != "release-1" {
		t.Fatalf("retained task release_id = %v, want release-1", releaseID)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET release_id=NULL WHERE id='task-1'`); err == nil || !containsSQLMessage(err, "release_frozen") {
		t.Fatalf("released membership mutation error = %v, want release_frozen", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET deleted_at='2026-01-04T00:00:00Z' WHERE id='task-1'`); err == nil || !containsSQLMessage(err, "release_frozen") {
		t.Fatalf("released task deletion error = %v, want release_frozen", err)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("rerun migration 022: %v", err)
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("migration 022 integrity: %v", err)
	}
}

func containsSQLMessage(err error, wanted string) bool {
	return err != nil && len(wanted) > 0 && strings.Contains(strings.ToLower(err.Error()), strings.ToLower(wanted))
}
