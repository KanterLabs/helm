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

	var beforeID, beforeProjectID, beforeColumnID, beforeTitle string
	var beforeTaskCount, beforeProjectCount, beforeColumnCount int
	if err := database.QueryRowContext(ctx, `SELECT id, project_id, column_id, title FROM tasks WHERE id='task-1'`).Scan(&beforeID, &beforeProjectID, &beforeColumnID, &beforeTitle); err != nil {
		t.Fatal(err)
	}
	for query, destination := range map[string]*int{
		`SELECT COUNT(*) FROM tasks`:    &beforeTaskCount,
		`SELECT COUNT(*) FROM projects`: &beforeProjectCount,
		`SELECT COUNT(*) FROM columns`:  &beforeColumnCount,
	} {
		if err := database.QueryRowContext(ctx, query).Scan(destination); err != nil {
			t.Fatal(err)
		}
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
	var afterID, afterProjectID, afterColumnID, afterTitle string
	if err := database.QueryRowContext(ctx, `SELECT id, project_id, column_id, title FROM tasks WHERE id='task-1'`).Scan(&afterID, &afterProjectID, &afterColumnID, &afterTitle); err != nil {
		t.Fatal(err)
	}
	if afterID != beforeID || afterProjectID != beforeProjectID || afterColumnID != beforeColumnID || afterTitle != beforeTitle {
		t.Fatalf("existing task changed during migration: before=%s/%s/%s/%s after=%s/%s/%s/%s", beforeID, beforeProjectID, beforeColumnID, beforeTitle, afterID, afterProjectID, afterColumnID, afterTitle)
	}
	for query, expected := range map[string]int{
		`SELECT COUNT(*) FROM tasks`:    beforeTaskCount,
		`SELECT COUNT(*) FROM projects`: beforeProjectCount,
		`SELECT COUNT(*) FROM columns`:  beforeColumnCount,
	} {
		var actual int
		if err := database.QueryRowContext(ctx, query).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if actual != expected {
			t.Fatalf("populated row count changed during migration for %q: got %d, want %d", query, actual, expected)
		}
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
	if err := ForeignKeyCheck(ctx, database); err != nil {
		t.Fatalf("migration 022 foreign keys: %v", err)
	}
}

// TestReleaseColumnLifecycleMigrationGuardsPopulated022Data exercises the
// 022 -> 023 boundary with a released task already in a completed column.
// UpdateColumn writes the column first and then derives task completion fields;
// migration 023 must reject that first write so the whole transaction remains
// unchanged, including when the writer is a retained pre-023 binary.
func TestReleaseColumnLifecycleMigrationGuardsPopulated022Data(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 23 {
		t.Skip("migration 023 is not present")
	}
	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, 22)
	populateProductionFixture(t, ctx, database, 22)

	if _, err := database.ExecContext(ctx, `INSERT INTO releases(id, project_id, name, description, version, created_at, updated_at) VALUES ('release-column-guard', 'project', '2.0', '', 1, '2026-02-01T00:00:00Z', '2026-02-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert planned release: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET release_id='release-column-guard' WHERE id='task-1'`); err != nil {
		t.Fatalf("assign release task: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO columns(id, project_id, name, semantic_state, position, created_at, updated_at) VALUES ('done-column-guard', 'project', 'Done', 'completed', 3, '2026-02-01T00:00:00Z', '2026-02-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert completed column: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET column_id='done-column-guard', completed_at='2026-02-02T00:00:00Z', claimed_by=NULL, claim_expires_at=NULL WHERE id='task-1'`); err != nil {
		t.Fatalf("complete release task: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE releases SET released_at='2026-02-02T00:00:00Z', released_by='owner' WHERE id='release-column-guard'`); err != nil {
		t.Fatalf("release completed task: %v", err)
	}

	type releaseSnapshot struct {
		taskID, projectID, columnID, releaseID, completedAt, taskUpdatedAt string
		columnState, columnUpdatedAt                                       string
		taskVersion, columnVersion                                         int64
		counts                                                             map[string]int
	}
	readSnapshot := func() releaseSnapshot {
		snapshot := releaseSnapshot{counts: make(map[string]int)}
		if err := database.QueryRowContext(ctx, `SELECT id, project_id, column_id, release_id, completed_at, updated_at, version FROM tasks WHERE id='task-1'`).Scan(&snapshot.taskID, &snapshot.projectID, &snapshot.columnID, &snapshot.releaseID, &snapshot.completedAt, &snapshot.taskUpdatedAt, &snapshot.taskVersion); err != nil {
			t.Fatalf("read release task snapshot: %v", err)
		}
		if err := database.QueryRowContext(ctx, `SELECT semantic_state, updated_at, version FROM columns WHERE id='done-column-guard'`).Scan(&snapshot.columnState, &snapshot.columnUpdatedAt, &snapshot.columnVersion); err != nil {
			t.Fatalf("read release column snapshot: %v", err)
		}
		for _, table := range []string{"tasks", "projects", "columns", "releases"} {
			var count int
			if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
				t.Fatalf("count %s: %v", table, err)
			}
			snapshot.counts[table] = count
		}
		return snapshot
	}

	beforeMigration := readSnapshot()
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate populated schema 22 through latest: %v", err)
	}
	var schemaVersion int
	if err := database.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	latest := migrations[len(migrations)-1].version
	if schemaVersion != latest {
		t.Fatalf("schema version = %d, want %d", schemaVersion, latest)
	}
	afterMigration := readSnapshot()
	if beforeMigration.taskID != afterMigration.taskID || beforeMigration.projectID != afterMigration.projectID || beforeMigration.columnID != afterMigration.columnID || beforeMigration.releaseID != afterMigration.releaseID || beforeMigration.completedAt != afterMigration.completedAt || beforeMigration.taskUpdatedAt != afterMigration.taskUpdatedAt || beforeMigration.taskVersion != afterMigration.taskVersion {
		t.Fatalf("migration changed released task: before=%s/%s/%s/%s/%s/%s/v%d after=%s/%s/%s/%s/%s/%s/v%d", beforeMigration.taskID, beforeMigration.projectID, beforeMigration.columnID, beforeMigration.releaseID, beforeMigration.completedAt, beforeMigration.taskUpdatedAt, beforeMigration.taskVersion, afterMigration.taskID, afterMigration.projectID, afterMigration.columnID, afterMigration.releaseID, afterMigration.completedAt, afterMigration.taskUpdatedAt, afterMigration.taskVersion)
	}
	if beforeMigration.columnState != afterMigration.columnState || beforeMigration.columnUpdatedAt != afterMigration.columnUpdatedAt || beforeMigration.columnVersion != afterMigration.columnVersion {
		t.Fatalf("migration changed release column: before=%s/%s/v%d after=%s/%s/v%d", beforeMigration.columnState, beforeMigration.columnUpdatedAt, beforeMigration.columnVersion, afterMigration.columnState, afterMigration.columnUpdatedAt, afterMigration.columnVersion)
	}
	for _, table := range []string{"tasks", "projects", "columns", "releases"} {
		if beforeMigration.counts[table] != afterMigration.counts[table] {
			t.Fatalf("migration changed %s count: before=%d after=%d", table, beforeMigration.counts[table], afterMigration.counts[table])
		}
	}

	var triggerCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='releases_guard_released_column_lifecycle'`).Scan(&triggerCount); err != nil {
		t.Fatalf("count release column guard: %v", err)
	}
	if triggerCount != 1 {
		t.Fatalf("release column guard count = %d, want 1", triggerCount)
	}

	// This is the same write ordering used by UpdateColumn: update the column,
	// then derive completion fields on its live tasks. The additive guard must
	// reject the first statement, leaving the transaction rollback-safe.
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, updateErr := tx.ExecContext(ctx, `UPDATE columns SET semantic_state='active', position=-1, version=version+1, updated_at='2026-02-03T00:00:00Z' WHERE id='done-column-guard'`)
	if updateErr == nil {
		_, updateErr = tx.ExecContext(ctx, `UPDATE tasks SET completed_at=NULL, updated_at='2026-02-03T00:00:00Z', version=version+1 WHERE column_id='done-column-guard' AND deleted_at IS NULL`)
	}
	if updateErr == nil || !containsSQLMessage(updateErr, "release_frozen") {
		_ = tx.Rollback()
		t.Fatalf("released column lifecycle write error = %v, want release_frozen", updateErr)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback rejected release column write: %v", err)
	}
	afterRejectedWrite := readSnapshot()
	if afterMigration.taskID != afterRejectedWrite.taskID || afterMigration.projectID != afterRejectedWrite.projectID || afterMigration.columnID != afterRejectedWrite.columnID || afterMigration.releaseID != afterRejectedWrite.releaseID || afterMigration.completedAt != afterRejectedWrite.completedAt || afterMigration.taskUpdatedAt != afterRejectedWrite.taskUpdatedAt || afterMigration.taskVersion != afterRejectedWrite.taskVersion {
		t.Fatalf("rejected column write changed released task: before=%s/%s/%s/%s/%s/%s/v%d after=%s/%s/%s/%s/%s/%s/v%d", afterMigration.taskID, afterMigration.projectID, afterMigration.columnID, afterMigration.releaseID, afterMigration.completedAt, afterMigration.taskUpdatedAt, afterMigration.taskVersion, afterRejectedWrite.taskID, afterRejectedWrite.projectID, afterRejectedWrite.columnID, afterRejectedWrite.releaseID, afterRejectedWrite.completedAt, afterRejectedWrite.taskUpdatedAt, afterRejectedWrite.taskVersion)
	}
	if afterMigration.columnState != afterRejectedWrite.columnState || afterMigration.columnUpdatedAt != afterRejectedWrite.columnUpdatedAt || afterMigration.columnVersion != afterRejectedWrite.columnVersion {
		t.Fatalf("rejected column write changed release column: before=%s/%s/v%d after=%s/%s/v%d", afterMigration.columnState, afterMigration.columnUpdatedAt, afterMigration.columnVersion, afterRejectedWrite.columnState, afterRejectedWrite.columnUpdatedAt, afterRejectedWrite.columnVersion)
	}
	for _, table := range []string{"tasks", "projects", "columns", "releases"} {
		if afterMigration.counts[table] != afterRejectedWrite.counts[table] {
			t.Fatalf("rejected column write changed %s count: before=%d after=%d", table, afterMigration.counts[table], afterRejectedWrite.counts[table])
		}
	}

	// A retained binary can still perform an unrelated task metadata write;
	// the new guard freezes lifecycle transitions rather than all task edits.
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET title='Retained release metadata write', version=version+1, updated_at='2026-02-03T00:01:00Z' WHERE id='task-1'`); err != nil {
		t.Fatalf("retained metadata write: %v", err)
	}
	var retainedReleaseID string
	if err := database.QueryRowContext(ctx, `SELECT release_id FROM tasks WHERE id='task-1'`).Scan(&retainedReleaseID); err != nil {
		t.Fatalf("read retained release membership: %v", err)
	}
	if retainedReleaseID != "release-column-guard" {
		t.Fatalf("retained task release_id = %q, want release-column-guard", retainedReleaseID)
	}

	// Re-executing the additive SQL is safe even after the migration has been
	// recorded, and a normal migration retry must not duplicate the trigger.
	if _, err := database.ExecContext(ctx, string(migrations[22].contents)); err != nil {
		t.Fatalf("rerun migration 023 SQL: %v", err)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("rerun migrations through latest: %v", err)
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("migration 023 integrity: %v", err)
	}
	if err := ForeignKeyCheck(ctx, database); err != nil {
		t.Fatalf("migration 023 foreign keys: %v", err)
	}
}

func containsSQLMessage(err error, wanted string) bool {
	return err != nil && len(wanted) > 0 && strings.Contains(strings.ToLower(err.Error()), strings.ToLower(wanted))
}
