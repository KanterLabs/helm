package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestMigration041PreservesPopulatedMembershipData exercises the upgrade from
// the production schema immediately before membership support. It deliberately
// inserts the legacy rows before migration 041 so the assertions cover
// backfill, rather than only an empty fresh database.
func TestMigration041PreservesPopulatedMembershipData(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 41 {
		t.Fatal("migration 041 is not present")
	}
	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, 40)
	populateProductionFixture(t, ctx, database, 40)
	const timestamp = "2026-01-01T00:00:00Z"
	if _, err := database.ExecContext(ctx, `INSERT INTO actors(id, kind, name, email, password_hash, admin, created_at, updated_at) VALUES ('member', 'human', 'Member', 'member@example.test', NULL, 0, ?, ?)`, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO actor_projects(actor_id, project_id) VALUES ('owner', 'project')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO sessions(id, actor_id, token_hash, expires_at, created_at, last_seen_at) VALUES ('legacy-session', 'owner', 'legacy-token-hash', '2027-01-01T00:00:00Z', ?, ?)`, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}

	before := migration041StableRows(t, ctx, database)
	stable := readStableFixture(t, ctx, database, 40)
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate through 041: %v", err)
	}

	inspection, err := InspectSchema(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.SchemaVersion != 41 || len(inspection.PendingVersions) != 0 || len(inspection.UnknownVersions) != 0 {
		t.Fatalf("schema inspection = %#v", inspection)
	}
	for _, table := range []string{"workspace_memberships", "project_memberships"} {
		assertTableExists(t, ctx, database, table)
	}
	for _, column := range []string{"revoked_at", "ip_address", "user_agent"} {
		assertColumnExists(t, ctx, database, "sessions", column)
	}

	var ownerRole, memberRole string
	if err := database.QueryRowContext(ctx, `SELECT role FROM workspace_memberships WHERE actor_id='owner'`).Scan(&ownerRole); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT role FROM workspace_memberships WHERE actor_id='member'`).Scan(&memberRole); err != nil {
		t.Fatal(err)
	}
	if ownerRole != "owner" || memberRole != "member" {
		t.Fatalf("backfilled roles = owner:%q member:%q", ownerRole, memberRole)
	}
	var humanGrant, agentGrant int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_memberships WHERE actor_id='owner' AND project_id='project'`).Scan(&humanGrant); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_memberships WHERE actor_id='agent'`).Scan(&agentGrant); err != nil {
		t.Fatal(err)
	}
	if humanGrant != 1 || agentGrant != 0 {
		t.Fatalf("project membership backfill = human:%d agent:%d", humanGrant, agentGrant)
	}

	after := migration041StableRows(t, ctx, database)
	assertStableFixtureMigration(t, stable, readStableFixture(t, ctx, database, 41))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("migration 041 changed legacy rows:\nbefore=%#v\nafter=%#v", before, after)
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("integrity after migration 041: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO project_memberships(actor_id, project_id, created_at, updated_at) VALUES ('missing-actor', 'project', ?, ?)`, timestamp, timestamp); err == nil {
		t.Fatal("project membership foreign key unexpectedly accepted a missing actor")
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO project_memberships(actor_id, project_id, created_at, updated_at) VALUES ('agent', 'project', ?, ?)`, timestamp, timestamp); err == nil {
		t.Fatal("project membership trigger unexpectedly accepted an agent actor")
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO workspace_memberships(actor_id, role, created_at, updated_at) VALUES ('agent', 'member', ?, ?)`, timestamp, timestamp); err == nil {
		t.Fatal("workspace membership trigger unexpectedly accepted an agent actor")
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO workspace_memberships(actor_id, role, created_at, updated_at) VALUES ('member', 'not-a-role', ?, ?)`, timestamp, timestamp); err == nil {
		t.Fatal("workspace role check unexpectedly accepted an unknown role")
	}
	if _, err := database.ExecContext(ctx, `UPDATE actors SET kind='agent' WHERE id='owner'`); err == nil {
		t.Fatal("actor kind update unexpectedly orphaned memberships")
	}

	// This SQL-shape probe does not prove retained-binary authorization parity.
	// A retained schema40 binary only knows the original six session columns. Reads
	// and writes through that shape must remain valid after the additive ALTER.
	var legacyID, legacyActor, legacyHash, legacyExpires, legacyCreated, legacySeen string
	if err := database.QueryRowContext(ctx, `SELECT id, actor_id, token_hash, expires_at, created_at, last_seen_at FROM sessions WHERE id='legacy-session'`).Scan(&legacyID, &legacyActor, &legacyHash, &legacyExpires, &legacyCreated, &legacySeen); err != nil {
		t.Fatalf("retained schema40 session read: %v", err)
	}
	if legacyID != "legacy-session" || legacyActor != "owner" || legacyHash != "legacy-token-hash" || legacyExpires != "2027-01-01T00:00:00Z" || legacyCreated != timestamp || legacySeen != timestamp {
		t.Fatalf("retained schema40 session row = %#v", []string{legacyID, legacyActor, legacyHash, legacyExpires, legacyCreated, legacySeen})
	}
	if _, err := database.ExecContext(ctx, `UPDATE sessions SET last_seen_at='2026-01-02T00:00:00Z' WHERE id='legacy-session'`); err != nil {
		t.Fatalf("retained schema40 session write: %v", err)
	}
	var roleAfterRetainedWrite string
	if err := database.QueryRowContext(ctx, `SELECT role FROM workspace_memberships WHERE actor_id='owner'`).Scan(&roleAfterRetainedWrite); err != nil {
		t.Fatal(err)
	}
	if roleAfterRetainedWrite != "owner" {
		t.Fatalf("retained schema40 write changed membership role to %q", roleAfterRetainedWrite)
	}

	after = migration041StableRows(t, ctx, database)
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("rerun migration 041: %v", err)
	}
	if rerun := migration041StableRows(t, ctx, database); !reflect.DeepEqual(after, rerun) {
		t.Fatalf("rerun changed legacy rows:\nafter=%#v\nrerun=%#v", after, rerun)
	}
	if dir := os.Getenv("HELM_TEST_ARTIFACT_DIR"); dir != "" {
		proof := map[string]any{"schema_before": 40, "schema_after": inspection.SchemaVersion, "stable_counts_before": before.Counts, "stable_counts_after": after.Counts, "stable_identifiers_credentials_claims_history": true, "owner_role": ownerRole, "legacy_session_shape_preserved": true, "migration_retry_stable": true, "integrity_and_foreign_keys": "passed", "retained_image_runtime_gate": "not_run_parent_release_gate"}
		raw, err := json.MarshalIndent(proof, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "schema40-upgrade-proof.json")
		if err = os.WriteFile(target, raw, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("sanitized populated upgrade proof: %s", target)
	}

}

func TestMigration041RollsBackDDLAndBackfillOnFailure(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 41 {
		t.Fatal("migration 041 is not present")
	}
	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, 40)
	populateProductionFixture(t, ctx, database, 40)
	// This deliberately simulates an interrupted/manual candidate where one
	// additive column exists but schema_migrations has not recorded 041. The
	// migration must roll back its new tables, triggers, and backfill when the
	// duplicate ALTER fails; a retry can then be made against a clean database.
	if _, err := database.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN revoked_at TEXT`); err != nil {
		t.Fatalf("prepare rollback fixture: %v", err)
	}
	if err := Migrate(ctx, database); err == nil {
		t.Fatal("migration 041 unexpectedly succeeded against duplicate column")
	}
	inspection, err := InspectSchema(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.SchemaVersion != 40 {
		t.Fatalf("failed migration recorded schema version %d, want 40", inspection.SchemaVersion)
	}
	for _, table := range []string{"workspace_memberships", "project_memberships"} {
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed migration left table %q behind", table)
		}
	}
	for _, column := range []string{"ip_address", "user_agent"} {
		if hasColumn := hasTableColumn(t, ctx, database, "sessions", column); hasColumn {
			t.Fatalf("failed migration left sessions.%s behind", column)
		}
	}
}

type migration041Rows struct {
	Actors   string
	Projects string
	Tasks    string
	Events   string
	Sessions int
	Counts   map[string]int
}

func migration041StableRows(t *testing.T, ctx context.Context, database *sql.DB) migration041Rows {
	t.Helper()
	rows := migration041Rows{}
	queries := []struct {
		name  string
		dest  *string
		query string
	}{
		{"actors", &rows.Actors, `SELECT group_concat(value, '|') FROM (SELECT id || ':' || kind || ':' || name || ':' || COALESCE(email,'') || ':' || COALESCE(password_hash,'') AS value FROM actors ORDER BY id)`},
		{"projects", &rows.Projects, `SELECT group_concat(value, '|') FROM (SELECT id || ':' || key || ':' || slug || ':' || name AS value FROM projects ORDER BY id)`},
		{"tasks", &rows.Tasks, `SELECT group_concat(value, '|') FROM (SELECT id || ':' || number || ':' || project_id || ':' || column_id || ':' || title AS value FROM tasks ORDER BY id)`},
		{"events", &rows.Events, `SELECT group_concat(value, '|') FROM (SELECT id || ':' || type || ':' || COALESCE(actor_id,'') || ':' || COALESCE(task_id,'') || ':' || payload AS value FROM events ORDER BY cursor)`},
	}
	for _, item := range queries {
		var value sql.NullString
		if err := database.QueryRowContext(ctx, item.query).Scan(&value); err != nil {
			t.Fatalf("read stable %s: %v", item.name, err)
		}
		if value.Valid {
			*item.dest = value.String
		}
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&rows.Sessions); err != nil {
		t.Fatal(err)
	}
	rows.Counts = make(map[string]int, 5)
	for _, table := range []string{"actors", "projects", "tasks", "events", "sessions"} {
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count stable %s: %v", table, err)
		}
		rows.Counts[table] = count
	}
	return rows
}

func assertTableExists(t *testing.T, ctx context.Context, database *sql.DB, table string) {
	t.Helper()
	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("table %q count = %d", table, count)
	}
}

func assertColumnExists(t *testing.T, ctx context.Context, database *sql.DB, table, column string) {
	t.Helper()
	rows, err := database.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("column %s.%s is missing", table, column)
}

func hasTableColumn(t *testing.T, ctx context.Context, database *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := database.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}
