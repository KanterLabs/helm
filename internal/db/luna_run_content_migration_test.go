package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestLunaRunContentMigrationFromPopulated027SupportsRetainedMetadataWrites(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "roadmap.db")
	database, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	applyMigrationPrefix(t, ctx, database, migrations, 27)
	populateProductionFixture(t, ctx, database, 27)
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate populated 027 database: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO luna_run_content(run_id,input_text,output_text,input_truncated,output_truncated,updated_at) VALUES ('luna-run-1','private input','partial output',0,1,'2026-01-01T00:01:00Z')`); err != nil {
		t.Fatalf("insert Luna content: %v", err)
	}
	var input, output string
	var truncated int
	if err := database.QueryRowContext(ctx, `SELECT input_text,output_text,output_truncated FROM luna_run_content WHERE run_id='luna-run-1'`).Scan(&input, &output, &truncated); err != nil || input != "private input" || output != "partial output" || truncated != 1 {
		t.Fatalf("migrated Luna content input=%q output=%q truncated=%d err=%v", input, output, truncated, err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE luna_runs SET detail='retained metadata write' WHERE id='luna-run-1'`); err != nil {
		t.Fatalf("retained metadata write: %v", err)
	}
	var detail string
	if err := database.QueryRowContext(ctx, `SELECT detail FROM luna_runs WHERE id='luna-run-1'`).Scan(&detail); err != nil || detail != "retained metadata write" {
		t.Fatalf("retained metadata detail=%q err=%v", detail, err)
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("migrated schema integrity: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen without migration to model a retained binary from the 027 schema.
	retained, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	var retainedDetail string
	if err := retained.QueryRowContext(ctx, `SELECT detail FROM luna_runs WHERE id='luna-run-1'`).Scan(&retainedDetail); err != nil || retainedDetail != "retained metadata write" {
		t.Fatalf("retained binary read detail=%q err=%v", retainedDetail, err)
	}
	if _, err := retained.ExecContext(ctx, `UPDATE luna_runs SET detail='older binary write' WHERE id='luna-run-1'`); err != nil {
		t.Fatalf("retained binary metadata write: %v", err)
	}
	var retainedContent string
	if err := retained.QueryRowContext(ctx, `SELECT output_text FROM luna_run_content WHERE run_id='luna-run-1'`).Scan(&retainedContent); err != nil || retainedContent != "partial output" {
		t.Fatalf("retained binary content=%q err=%v", retainedContent, err)
	}
}
