package db

import (
	"context"
	"reflect"
	"testing"
)

func TestEmbeddedSchemaIdentityIsStable(t *testing.T) {
	version, digest, err := EmbeddedSchema()
	if err != nil {
		t.Fatalf("embedded schema: %v", err)
	}
	if version < 10 {
		t.Fatalf("latest schema version = %d, want at least 10", version)
	}
	if len(digest) != 64 {
		t.Fatalf("migration digest length = %d, want 64", len(digest))
	}
	versionAgain, digestAgain, err := EmbeddedSchema()
	if err != nil {
		t.Fatalf("embedded schema second read: %v", err)
	}
	if versionAgain != version || digestAgain != digest {
		t.Fatalf("embedded schema changed between reads: (%d, %s), (%d, %s)", version, digest, versionAgain, digestAgain)
	}
	if got, err := LatestEmbeddedSchemaVersion(); err != nil || got != version {
		t.Fatalf("latest embedded version = %d, %v; want %d", got, err, version)
	}
	if got, err := EmbeddedMigrationDigest(); err != nil || got != digest {
		t.Fatalf("embedded migration digest = %s, %v; want %s", got, err, digest)
	}
}

func TestMigrationVersionValidation(t *testing.T) {
	for _, name := range []string{"migration.sql", "0_bad.sql", "x_bad.sql", "01.sql"} {
		if _, err := migrationVersion(name); err == nil {
			t.Errorf("migrationVersion(%q) unexpectedly succeeded", name)
		}
	}
	for _, test := range []struct {
		name string
		want int
	}{
		{name: "001_init.sql", want: 1},
		{name: "009_additive_change.sql", want: 9},
		{name: "010_board_audits.sql", want: 10},
	} {
		if got, err := migrationVersion(test.name); err != nil || got != test.want {
			t.Errorf("migrationVersion(%q) = %d, %v; want %d", test.name, got, err, test.want)
		}
	}
}

func TestInspectSchemaReportsLatestPendingAndUnknownVersions(t *testing.T) {
	ctx := context.Background()
	latest, _, err := EmbeddedSchema()
	if err != nil {
		t.Fatal(err)
	}
	if latest < 10 {
		t.Fatalf("latest schema version = %d, want schema 10", latest)
	}

	t.Run("latest", func(t *testing.T) {
		database := newRawDatabase(t)
		if err := Migrate(ctx, database); err != nil {
			t.Fatalf("migrate latest schema: %v", err)
		}
		inspection, err := InspectSchema(ctx, database)
		if err != nil {
			t.Fatal(err)
		}
		if inspection.SchemaVersion != latest || !reflect.DeepEqual(inspection.AppliedVersions, versionsThrough(latest)) || len(inspection.PendingVersions) != 0 || len(inspection.UnknownVersions) != 0 {
			t.Fatalf("latest schema inspection = %#v", inspection)
		}
	})

	t.Run("pending", func(t *testing.T) {
		database := newRawDatabase(t)
		migrations, err := embeddedMigrations()
		if err != nil {
			t.Fatal(err)
		}
		applyMigrationPrefix(t, ctx, database, migrations, latest-1)
		inspection, err := InspectSchema(ctx, database)
		if err != nil {
			t.Fatal(err)
		}
		if inspection.SchemaVersion != latest-1 || !reflect.DeepEqual(inspection.AppliedVersions, versionsThrough(latest-1)) || !reflect.DeepEqual(inspection.PendingVersions, []int{latest}) || len(inspection.UnknownVersions) != 0 {
			t.Fatalf("pending schema inspection = %#v", inspection)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		database := newRawDatabase(t)
		migrations, err := embeddedMigrations()
		if err != nil {
			t.Fatal(err)
		}
		applyMigrationPrefix(t, ctx, database, migrations, latest)
		const unknownVersion = 999
		if _, err := database.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, 'future')`, unknownVersion); err != nil {
			t.Fatal(err)
		}
		inspection, err := InspectSchema(ctx, database)
		if err != nil {
			t.Fatal(err)
		}
		if inspection.SchemaVersion != unknownVersion || !reflect.DeepEqual(inspection.AppliedVersions, append(versionsThrough(latest), unknownVersion)) || len(inspection.PendingVersions) != 0 || !reflect.DeepEqual(inspection.UnknownVersions, []int{unknownVersion}) {
			t.Fatalf("unknown schema inspection = %#v", inspection)
		}
	})
}

func versionsThrough(latest int) []int {
	versions := make([]int, latest)
	for index := range versions {
		versions[index] = index + 1
	}
	return versions
}

func TestMigrateLeavesUnknownNewerVersionForRollbackCompatibility(t *testing.T) {
	ctx := context.Background()
	database := newRawDatabase(t)
	if _, err := database.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL); INSERT INTO schema_migrations(version, applied_at) VALUES (999, 'future');`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate database with unknown newer version: %v", err)
	}
	var ownTables int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='actors'`).Scan(&ownTables); err != nil {
		t.Fatal(err)
	}
	if ownTables != 0 {
		t.Fatalf("retained migration created local tables in a newer-schema database: %d", ownTables)
	}
	inspection, err := InspectSchema(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if !containsInt(inspection.UnknownVersions, 999) {
		t.Fatalf("unknown versions = %v, want 999", inspection.UnknownVersions)
	}
}
