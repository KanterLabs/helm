package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Migrations is intentionally embedded in the binary so a deployment never
// depends on a writable source tree or a migration volume being mounted.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// SchemaInspection describes the migration state observed in a database and
// the migration set embedded in this binary. Unknown versions are reported,
// but are deliberately not treated as an error: retained binaries must be
// able to open a database upgraded by a newer release for rollback/recovery.
// The explicit Preflight operation below is the fail-closed path for a
// candidate database.
type SchemaInspection struct {
	// SchemaVersion is the greatest version recorded in schema_migrations. A
	// database with no migration table or rows has version zero.
	SchemaVersion int
	// AppliedVersions is sorted numerically and includes versions unknown to
	// this binary.
	AppliedVersions []int
	// EmbeddedSchemaVersion and MigrationDigest describe this binary's
	// embedded migration set.
	EmbeddedSchemaVersion int
	MigrationDigest       string
	// PendingVersions are embedded migrations not recorded in the database.
	PendingVersions []int
	// UnknownVersions are recorded in the database but absent from this
	// binary's embedded migration set.
	UnknownVersions []int
}

type embeddedMigration struct {
	name     string
	version  int
	contents []byte
}

// embeddedMigrations returns the complete embedded migration set in numeric
// order. It validates the set before any database mutation so a malformed
// release cannot create bookkeeping state and then fail halfway through
// startup. Duplicate versions are rejected even when their filenames differ.
func embeddedMigrations() ([]embeddedMigration, error) {
	entries, err := fs.ReadDir(Migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	migrations := make([]embeddedMigration, 0, len(entries))
	versions := make(map[int]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return nil, err
		}
		if prior, exists := versions[version]; exists {
			return nil, fmt.Errorf("duplicate embedded migration version %d (%s and %s)", version, prior, entry.Name())
		}
		contents, err := fs.ReadFile(Migrations, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		versions[version] = entry.Name()
		migrations = append(migrations, embeddedMigration{name: entry.Name(), version: version, contents: contents})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no embedded SQL migrations found")
	}
	sort.Slice(migrations, func(i, j int) bool {
		if migrations[i].version == migrations[j].version {
			return migrations[i].name < migrations[j].name
		}
		return migrations[i].version < migrations[j].version
	})
	for index, migration := range migrations {
		expected := index + 1
		if migration.version != expected {
			return nil, fmt.Errorf("embedded migration versions must be contiguous from 1 (found %d, want %d)", migration.version, expected)
		}
	}
	return migrations, nil
}

// EmbeddedSchema returns the latest version and deterministic SHA-256 digest
// of this binary's embedded migration set. The digest includes each numeric
// version, filename, byte length, and exact SQL bytes in numeric order, so a
// changed migration cannot silently share an old identity.
func EmbeddedSchema() (int, string, error) {
	migrations, err := embeddedMigrations()
	if err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	for _, migration := range migrations {
		// Length-prefixing prevents concatenation ambiguities while retaining a
		// simple, reproducible representation suitable for sidecar metadata.
		if _, err := fmt.Fprintf(hash, "%d\n%d\n%s\n", migration.version, len(migration.contents), migration.name); err != nil {
			return 0, "", fmt.Errorf("hash migration %s: %w", migration.name, err)
		}
		if _, err := hash.Write(migration.contents); err != nil {
			return 0, "", fmt.Errorf("hash migration %s: %w", migration.name, err)
		}
		if _, err := hash.Write([]byte{'\n'}); err != nil {
			return 0, "", fmt.Errorf("hash migration %s: %w", migration.name, err)
		}
	}
	return migrations[len(migrations)-1].version, fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// LatestEmbeddedSchemaVersion exposes the latest valid version in the
// embedded migration set.
func LatestEmbeddedSchemaVersion() (int, error) {
	version, _, err := EmbeddedSchema()
	return version, err
}

// EmbeddedMigrationDigest exposes the deterministic migration-set digest.
func EmbeddedMigrationDigest() (string, error) {
	_, digest, err := EmbeddedSchema()
	return digest, err
}

// Migrate applies embedded migrations in version order. Each complete
// migration is atomic and can safely be retried after an interrupted startup.
// An unknown newer database version is intentionally left in place so a
// retained older binary can still start for rollback/recovery; schema-preflight
// is the explicit fail-closed candidate check.
func Migrate(ctx context.Context, database *sql.DB) error {
	migrations, err := embeddedMigrations()
	if err != nil {
		return err
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	if err := validateAppliedMigrationRows(ctx, database); err != nil {
		return err
	}
	var highestApplied int64
	if err := database.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&highestApplied); err != nil {
		return fmt.Errorf("inspect latest applied migration: %w", err)
	}
	// A retained binary must not try to fill in its own missing bookkeeping or
	// objects when a newer release has already recorded a schema version. The
	// newer release owns that format; leaving the database untouched is the
	// rollback-compatible behavior.
	if highestApplied > int64(migrations[len(migrations)-1].version) {
		return nil
	}
	for _, migration := range migrations {
		var applied int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, migration.version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", migration.version, err)
		}
		if applied != 0 {
			continue
		}
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, string(migration.contents)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", migration.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, migration.version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("commit migration %d: %w", migration.version, err)
		}
	}
	return nil
}

func validateAppliedMigrationRows(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("inspect applied migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return fmt.Errorf("inspect applied migration: %w", err)
		}
		if version <= 0 || version > int64(^uint(0)>>1) {
			return fmt.Errorf("invalid applied migration version %d", version)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect applied migrations: %w", err)
	}
	return nil
}

// InspectSchema reads migration bookkeeping without changing the database.
// It is safe to call against a database created by a newer release; such
// versions are surfaced through UnknownVersions instead of being rejected.
func InspectSchema(ctx context.Context, database *sql.DB) (SchemaInspection, error) {
	latest, digest, err := EmbeddedSchema()
	if err != nil {
		return SchemaInspection{}, err
	}
	inspection := SchemaInspection{
		EmbeddedSchemaVersion: latest,
		MigrationDigest:       digest,
		AppliedVersions:       []int{},
		PendingVersions:       []int{},
		UnknownVersions:       []int{},
	}

	var migrationTable int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&migrationTable); err != nil {
		return SchemaInspection{}, fmt.Errorf("inspect migration table: %w", err)
	}
	if migrationTable == 0 {
		for version := range embeddedVersionSet() {
			inspection.PendingVersions = append(inspection.PendingVersions, version)
		}
		sort.Ints(inspection.PendingVersions)
		return inspection, nil
	}

	rows, err := database.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return SchemaInspection{}, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	embedded := embeddedVersionSet()
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return SchemaInspection{}, fmt.Errorf("read applied migration: %w", err)
		}
		if version <= 0 || version > int64(^uint(0)>>1) {
			return SchemaInspection{}, fmt.Errorf("invalid applied migration version %d", version)
		}
		v := int(version)
		inspection.AppliedVersions = append(inspection.AppliedVersions, v)
		if v > inspection.SchemaVersion {
			inspection.SchemaVersion = v
		}
		if !embedded[v] {
			inspection.UnknownVersions = append(inspection.UnknownVersions, v)
		}
	}
	if err := rows.Err(); err != nil {
		return SchemaInspection{}, fmt.Errorf("read applied migrations: %w", err)
	}
	for version := range embedded {
		if !containsVersion(inspection.AppliedVersions, version) {
			inspection.PendingVersions = append(inspection.PendingVersions, version)
		}
	}
	sort.Ints(inspection.PendingVersions)
	sort.Ints(inspection.UnknownVersions)
	return inspection, nil
}

func embeddedVersionSet() map[int]bool {
	migrations, err := embeddedMigrations()
	if err != nil {
		return map[int]bool{}
	}
	versions := make(map[int]bool, len(migrations))
	for _, migration := range migrations {
		versions[migration.version] = true
	}
	return versions
}

func containsVersion(versions []int, wanted int) bool {
	for _, version := range versions {
		if version == wanted {
			return true
		}
	}
	return false
}

func migrationVersion(name string) (int, error) {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	underscore := strings.IndexByte(name, '_')
	if underscore <= 0 {
		return 0, fmt.Errorf("invalid migration filename %q", name)
	}
	version, err := strconv.Atoi(name[:underscore])
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("invalid migration version %q", name)
	}
	return version, nil
}
