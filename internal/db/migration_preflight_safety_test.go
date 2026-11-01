package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPreflightCopiesAndCleansPrivateDatabase(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "candidate.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationPrefix(t, ctx, database, migrations, 1)
	populateProductionFixture(t, ctx, database, 1)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := fileBytes(path)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := Preflight(ctx, path)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	latest, _, _ := EmbeddedSchema()
	if inspection.SchemaVersion != latest {
		t.Fatalf("preflight schema version = %d, want %d", inspection.SchemaVersion, latest)
	}
	after, err := fileBytes(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preflight modified its source database")
	}
	if matches, err := filepath.Glob(filepath.Join(dir, ".roadmap-preflight-*")); err != nil || len(matches) != 0 {
		t.Fatalf("preflight temporary files remain: %v", matches)
	}
}

func TestFailedMigrationRollsBackAndCanRetry(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, 1)
	// Migration 002 is intentionally made to fail after its CREATE IF NOT
	// EXISTS statement is bypassed. Its INSERT must not leave version 2 behind.
	if _, err := database.ExecContext(ctx, `DROP TABLE auth_setup; CREATE TABLE auth_setup (id INTEGER PRIMARY KEY);`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, database); err == nil {
		t.Fatal("Migrate unexpectedly succeeded with malformed auth_setup")
	}
	var applied int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=2`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Fatalf("failed migration recorded version 2 = %d", applied)
	}
	if _, err := database.ExecContext(ctx, `DROP TABLE auth_setup`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("retry after failed migration: %v", err)
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("retry integrity: %v", err)
	}
}
