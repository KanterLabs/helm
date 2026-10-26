package db

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestProductionShapedPrefixesMigrateWithoutChangingStableData(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	latest, _, err := EmbeddedSchema()
	if err != nil {
		t.Fatal(err)
	}
	for prefix := 1; prefix <= latest; prefix++ {
		prefix := prefix
		t.Run(fmt.Sprintf("prefix_%03d", prefix), func(t *testing.T) {
			database := newRawDatabase(t)
			applyMigrationPrefix(t, ctx, database, migrations, prefix)
			populateProductionFixture(t, ctx, database, prefix)
			before := readStableFixture(t, ctx, database, prefix)
			beforeAudits := readAuditFixture(t, ctx, database)
			if err := Migrate(ctx, database); err != nil {
				database.Close()
				t.Fatalf("migrate prefix %d: %v", prefix, err)
			}
			after := readStableFixture(t, ctx, database, latest)
			assertStableFixtureMigration(t, before, after)
			if got := after.counts["actors"]; got != 2 {
				t.Fatalf("actor count = %d, want 2", got)
			}
			if got := after.counts["tasks"]; got != 2 {
				t.Fatalf("task count = %d, want 2", got)
			}
			if got := after.counts["comments"]; got != 1 {
				t.Fatalf("comment count = %d, want 1", got)
			}
			if got := after.counts["events"]; got != 1 {
				t.Fatalf("event count = %d, want 1", got)
			}
			afterAudits := readAuditFixture(t, ctx, database)
			if afterAudits.runs == "<absent>" || afterAudits.findings == "<absent>" {
				t.Fatalf("migration %d did not create audit tables: %#v", prefix, afterAudits)
			}
			if prefix >= 10 && !reflect.DeepEqual(beforeAudits, afterAudits) {
				t.Fatalf("audit fixture changed during migration %d:\nbefore=%#v\nafter=%#v", prefix, beforeAudits, afterAudits)
			}
			inspection, err := InspectSchema(ctx, database)
			if err != nil {
				t.Fatalf("inspect migrated schema: %v", err)
			}
			if inspection.SchemaVersion != latest || len(inspection.PendingVersions) != 0 || len(inspection.UnknownVersions) != 0 {
				t.Fatalf("migrated schema inspection = %#v", inspection)
			}
			if err := CheckIntegrity(ctx, database); err != nil {
				t.Fatalf("migrated schema integrity: %v", err)
			}
			assertMigratedAdditiveValues(t, ctx, database, prefix)
			appliedBefore := after.counts["schema_migrations"]
			if err := Migrate(ctx, database); err != nil {
				t.Fatalf("rerun migration: %v", err)
			}
			afterRerun := readStableFixture(t, ctx, database, latest)
			assertStableFixtureMigration(t, after, afterRerun)
			afterRerunAudits := readAuditFixture(t, ctx, database)
			if afterRerun.counts["schema_migrations"] != appliedBefore || !reflect.DeepEqual(after.stable, afterRerun.stable) || !reflect.DeepEqual(afterAudits, afterRerunAudits) {
				t.Fatalf("rerun changed migration/data state: before=%#v after=%#v", after, afterRerun)
			}
		})
	}
}

func TestLunaRunStepsMigrationPreservesPopulatedRunHistory(t *testing.T) {
	ctx := context.Background()
	database := newRawDatabase(t)
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationPrefix(t, ctx, database, migrations, 26)
	populateProductionFixture(t, ctx, database, 26)
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate populated Luna history: %v", err)
	}
	var runCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM luna_runs WHERE id='luna-run-1'`).Scan(&runCount); err != nil || runCount != 1 {
		t.Fatalf("retained Luna run count=%d err=%v", runCount, err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO luna_run_steps(id,run_id,sequence,kind,at) VALUES ('step-1','luna-run-1',1,'thread_started','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert migrated Luna step: %v", err)
	}
	var kind string
	if err := database.QueryRowContext(ctx, `SELECT kind FROM luna_run_steps WHERE run_id='luna-run-1' AND sequence=1`).Scan(&kind); err != nil || kind != "thread_started" {
		t.Fatalf("migrated Luna step kind=%q err=%v", kind, err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM luna_runs WHERE id='luna-run-1'`); err != nil {
		t.Fatalf("delete retained Luna run: %v", err)
	}
	var steps int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM luna_run_steps WHERE run_id='luna-run-1'`).Scan(&steps); err != nil || steps != 0 {
		t.Fatalf("cascaded Luna steps=%d err=%v", steps, err)
	}
}
