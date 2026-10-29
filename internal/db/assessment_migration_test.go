package db

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

// Failure inventory: populated legacy history loses rows or links; additive
// field has no safe default; retained SQL stops working; retry duplicates data.
func TestAssessmentMigrationPreservesPopulatedLegacyHistory(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	database.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`)
	for _, m := range migrations {
		if m.version >= 41 {
			break
		}
		if _, err = database.ExecContext(ctx, string(m.contents)); err != nil {
			t.Fatal(err)
		}
		database.ExecContext(ctx, `INSERT INTO schema_migrations VALUES(?,'synthetic')`, m.version)
	}
	fixtureExec := func(q string) {
		t.Helper()
		if _, e := database.ExecContext(ctx, q); e != nil {
			t.Fatal(e)
		}
	}
	fixtureExec(`INSERT INTO actors(id,kind,name,created_at,updated_at) VALUES('owner','human','Synthetic owner','now','now')`)
	fixtureExec(`INSERT INTO projects(id,key,slug,name,created_at,updated_at) VALUES('p','MIG','mig','Synthetic project','now','now')`)
	fixtureExec(`INSERT INTO columns(id,project_id,name,semantic_state,position,created_at,updated_at) VALUES('c','p','Backlog','backlog',1,'now','now')`)
	fixtureExec(`INSERT INTO tasks(id,project_id,number,column_id,title,description,priority,position,created_at,updated_at) VALUES('t','p',1,'c','Synthetic task','Preserve','normal',1,'now','now')`)
	fixtureExec(`INSERT INTO comments(id,task_id,actor_id,body,created_at,updated_at) VALUES('comment','t','owner','Synthetic preserved comment','now','now')`)
	fixtureExec(`INSERT INTO audit_runs(id,project_id,actor_id,scope,status,started_at,created_at,updated_at) VALUES('r','p','owner','external','running','now','now','now')`)
	fixtureExec(`INSERT INTO audit_findings(id,audit_id,task_id,captured_version,source_column,verdict,confidence,reason,created_at,updated_at) VALUES('f','r','t',1,'c','needs_attention',0.5,'Legacy reason','now','now')`)
	os.MkdirAll("/tmp/helm-tc308-handoff", 0700)
	backup := "/tmp/helm-tc308-handoff/populated-v40.sqlite"
	os.Remove(backup)
	if _, err = database.ExecContext(ctx, `VACUUM INTO ?`, backup); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	for _, invalidAssessment := range []string{`{}`, `{"assessment_version":1}`, `null`} {
		if _, err = database.ExecContext(ctx, `UPDATE audit_findings SET assessment_json=? WHERE id='f'`, invalidAssessment); err == nil {
			t.Fatal("storage accepted a malformed assessment version")
		}
	}
	var legacy sql.NullString
	var reason string
	if err = database.QueryRowContext(ctx, `SELECT assessment_json,reason FROM audit_findings WHERE id='f'`).Scan(&legacy, &reason); err != nil {
		t.Fatal(err)
	}
	if legacy.Valid || reason != "Legacy reason" {
		t.Fatal("legacy row reinterpreted")
	}
	// Exact old insert/update SQL remains usable against the upgraded schema.
	fixtureExec(`INSERT INTO audit_runs(id,project_id,actor_id,scope,status,started_at,created_at,updated_at) VALUES('r2','p','owner','external','running','now','now','now')`)
	fixtureExec(`INSERT INTO audit_findings(id,audit_id,task_id,captured_version,source_column,verdict,confidence,reason,created_at,updated_at) VALUES('g','r2','t',1,'c','correct',0.5,'Retained SQL','now','now')`)
	fixtureExec(`UPDATE audit_findings SET review_state='dismissed',version=version+1 WHERE id='f'`)
	if err = Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	var integrity string
	database.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity)
	if integrity != "ok" {
		t.Fatal(integrity)
	}
	var comments int
	database.QueryRowContext(ctx, `SELECT count(*) FROM comments WHERE task_id='t'`).Scan(&comments)
	if comments != 1 {
		t.Fatal("lost comments")
	}
	snapshot := "/tmp/helm-tc308-handoff/populated-v41.sqlite"
	os.Remove(snapshot)
	if _, err = database.ExecContext(ctx, `VACUUM INTO ?`, snapshot); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll("/tmp/helm-tc308-handoff", 0700)
	os.WriteFile("/tmp/helm-tc308-handoff/migration-receipt.json", []byte(`{"populated_from_schema":40,"target_schema":41,"legacy_assessment":null,"old_insert_update":"passed","migration_retry":"passed","integrity":"ok","comments_preserved":1}`), 0600)
}
