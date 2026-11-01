package db

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Column snapshots retain identity, names, lifecycle mappings, ordering and
// versions separately from data that remains byte-for-byte stable. Migration
// 036 intentionally inserts one column and shifts later column positions.
type migrationColumnSnapshot struct {
	ID, ProjectID, Name, State, CreatedAt, UpdatedAt string
	Position                                         int
	Version, OrderingVersion                         int64
	ArchivedAt                                       sql.NullString
}

func readMigrationColumns(t *testing.T, ctx context.Context, database *sql.DB, schemaVersion int) []migrationColumnSnapshot {
	t.Helper()
	versionFields := "1, NULL"
	if schemaVersion >= 13 {
		versionFields = "version, archived_at"
	}
	orderingField := "1"
	if schemaVersion >= 19 {
		orderingField = "ordering_version"
	}
	rows, err := database.QueryContext(ctx, `SELECT id, project_id, name, semantic_state, position, created_at, updated_at, `+versionFields+`, `+orderingField+` FROM columns ORDER BY project_id, position, id`)
	if err != nil {
		t.Fatalf("read retained column snapshots: %v", err)
	}
	defer rows.Close()
	var columns []migrationColumnSnapshot
	for rows.Next() {
		var column migrationColumnSnapshot
		if err := rows.Scan(&column.ID, &column.ProjectID, &column.Name, &column.State, &column.Position, &column.CreatedAt, &column.UpdatedAt, &column.Version, &column.ArchivedAt, &column.OrderingVersion); err != nil {
			t.Fatalf("scan retained column: %v", err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate retained columns: %v", err)
	}
	return columns
}

// assertStableFixtureMigration permits exactly migration 036's documented
// column insertion. Every retained column must keep its identity, name,
// mapping, timestamps and revisions unless its position actually shifts; a
// shifted column advances position/version once and receives a valid timestamp.
func assertStableFixtureMigration(t *testing.T, before, after stableFixture) {
	t.Helper()
	if !reflect.DeepEqual(before.stable, after.stable) {
		t.Fatalf("stable fixture changed during migration:\nbefore=%#v\nafter=%#v", before.stable, after.stable)
	}
	insertions := map[string]int{}
	if before.schemaVersion < 36 && after.schemaVersion >= 36 {
		for _, projectID := range before.projectIDs {
			insertion, maximum, foundBacklog, foundBugs := 0, -1, false, false
			for _, column := range before.columns {
				if column.ProjectID != projectID || column.ArchivedAt.Valid {
					continue
				}
				if column.Position > maximum {
					maximum = column.Position
				}
				isBugs := strings.EqualFold(strings.TrimSpace(column.Name), "Bugs")
				if column.State == "backlog" && isBugs {
					foundBugs = true
				}
				if column.State == "backlog" && !isBugs && (!foundBacklog || column.Position+1 < insertion) {
					insertion, foundBacklog = column.Position+1, true
				}
			}
			if !foundBugs {
				if !foundBacklog {
					insertion = maximum + 1
				}
				insertions[projectID] = insertion
			}
		}
	}
	afterByID := map[string]migrationColumnSnapshot{}
	for _, column := range after.columns {
		afterByID[column.ID] = column
	}
	for _, previous := range before.columns {
		current, found := afterByID[previous.ID]
		if !found {
			t.Fatalf("migration removed retained column %s", previous.ID)
		}
		delete(afterByID, previous.ID)
		expected := previous
		if insertion, inserted := insertions[previous.ProjectID]; inserted && !previous.ArchivedAt.Valid && previous.Position >= insertion {
			expected.Position++
			expected.Version++
			if current.UpdatedAt == previous.UpdatedAt {
				t.Fatalf("shifted column %s retained its old timestamp", previous.ID)
			}
			if _, err := time.Parse(time.RFC3339Nano, current.UpdatedAt); err != nil {
				t.Fatalf("shifted column %s has invalid timestamp: %v", previous.ID, err)
			}
			expected.UpdatedAt = current.UpdatedAt
		}
		if current != expected {
			t.Fatalf("retained column changed beyond intended insertion: before=%+v after=%+v expected=%+v", previous, current, expected)
		}
	}
	if after.counts["columns"] != before.counts["columns"]+len(insertions) || len(afterByID) != len(insertions) {
		t.Fatalf("column count after Bugs insertion: before=%d after=%d added=%d expected additions=%d", before.counts["columns"], after.counts["columns"], len(afterByID), len(insertions))
	}
	for _, column := range afterByID {
		position, expected := insertions[column.ProjectID]
		if !expected || column.Name != "Bugs" || column.State != "backlog" || column.Position != position || column.ArchivedAt.Valid || column.Version != 1 || column.OrderingVersion != 1 || column.ID == "" || column.CreatedAt != column.UpdatedAt {
			t.Fatalf("unexpected additive column: %+v; expected project positions=%v", column, insertions)
		}
		if _, err := time.Parse(time.RFC3339Nano, column.CreatedAt); err != nil {
			t.Fatalf("new Bugs column has invalid timestamp: %v", err)
		}
		delete(insertions, column.ProjectID)
	}
	if len(insertions) != 0 {
		t.Fatalf("projects missing their new Bugs column: %v", insertions)
	}
}
