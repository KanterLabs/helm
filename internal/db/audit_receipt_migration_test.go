package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Failure inventory: docs/AUDIT_CORRECTION_FAILURES.md, before migration code.
func TestAuditCorrectionReceiptPopulatedMigration(t *testing.T) {
	ctx := context.Background()
	m, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	d := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, d, m, 41)
	populateProductionFixture(t, ctx, d, 41)
	beforeHistory := readAuditFixture(t, ctx, d)
	os.MkdirAll("/tmp/helm-tc309-handoff", 0700)
	backup := "/tmp/helm-tc309-handoff/populated-v41.sqlite"
	if err = os.Remove(backup); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err = d.Exec(`VACUUM INTO ?`, backup); err != nil {
		t.Fatal(err)
	}
	copy, err := sql.Open("sqlite", "file:"+backup+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckIntegrity(ctx, copy); err != nil {
		t.Fatal(err)
	}
	var backupVersion int
	if err = copy.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&backupVersion); err != nil || backupVersion != 41 {
		t.Fatal("backup version", err, backupVersion)
	}
	if !reflect.DeepEqual(beforeHistory, readAuditFixture(t, ctx, copy)) {
		t.Fatal("backup history mismatch")
	}
	copy.Close()
	var tasks, comments int
	d.QueryRow(`SELECT count(*) FROM tasks`).Scan(&tasks)
	d.QueryRow(`SELECT count(*) FROM comments`).Scan(&comments)
	if err = Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	var receipts int
	if err = d.QueryRow(`SELECT count(*) FROM github_audit_correction_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	// Retained v41 task writer remains usable against the additive schema.
	if _, err = d.Exec(`UPDATE tasks SET title='Retained writer',version=version+1 WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	var n, c int
	d.QueryRow(`SELECT count(*) FROM tasks`).Scan(&n)
	d.QueryRow(`SELECT count(*) FROM comments`).Scan(&c)
	if n != tasks || c != comments {
		t.Fatal("populated history changed")
	}
	if !reflect.DeepEqual(beforeHistory, readAuditFixture(t, ctx, d)) {
		t.Fatal("migration changed legacy history")
	}
	if err = CheckIntegrity(ctx, d); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectSchema(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	upgraded := "/tmp/helm-tc309-handoff/populated-v42.sqlite"
	if err = os.Remove(upgraded); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err = d.Exec(`VACUUM INTO ?`, upgraded); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(map[string]any{"tasks_preserved": n, "comments_preserved": c, "receipts": receipts, "integrity": "ok", "retained_sql_write": "ok", "verified_preupgrade_backup": backup, "backup_schema": backupVersion, "history_preserved": true, "inspection": inspection}, "", "  ")
	os.MkdirAll("/tmp/helm-tc309-handoff", 0700)
	if err = os.WriteFile("/tmp/helm-tc309-handoff/migration.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
}
