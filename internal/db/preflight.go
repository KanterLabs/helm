package db

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// IntegrityCheck runs both SQLite consistency checks. A foreign-key check is
// intentionally separate from integrity_check because SQLite's latter does
// not guarantee that all relational references are valid.
func IntegrityCheck(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return fmt.Errorf("sqlite integrity check: %w", err)
	}
	defer rows.Close()
	seen := false
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return fmt.Errorf("sqlite integrity check: %w", err)
		}
		seen = true
		if result != "ok" {
			return fmt.Errorf("sqlite integrity check failed: %s", result)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite integrity check: %w", err)
	}
	if !seen {
		return fmt.Errorf("sqlite integrity check returned no result")
	}
	return nil
}

func ForeignKeyCheck(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("sqlite foreign key check: %w", err)
	}
	defer rows.Close()
	violations := 0
	for rows.Next() {
		// The pragma currently returns four columns. Scanning into []any keeps
		// this check tolerant of SQLite-compatible drivers adding a detail while
		// still requiring that every row be consumed.
		columns, err := rows.Columns()
		if err != nil {
			return fmt.Errorf("sqlite foreign key check: %w", err)
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return fmt.Errorf("sqlite foreign key check: %w", err)
		}
		violations++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite foreign key check: %w", err)
	}
	if violations != 0 {
		return fmt.Errorf("sqlite foreign key check found %d violation(s)", violations)
	}
	return nil
}

// CheckIntegrity is a convenient combined consistency check for callers that
// need the same predicate as backup/restore helpers.
func CheckIntegrity(ctx context.Context, database *sql.DB) error {
	if err := IntegrityCheck(ctx, database); err != nil {
		return err
	}
	return ForeignKeyCheck(ctx, database)
}

// Preflight copies sourcePath into a private temporary database, migrates and
// validates only that copy, and removes it before returning. It never opens
// the supplied path through the writable application setup, so production
// data cannot be migrated by this operation. The source must be a standalone
// SQLite file; callers should use helm-backup's online backup first when a
// live database has a WAL sidecar.
func Preflight(ctx context.Context, sourcePath string) (SchemaInspection, error) {
	absolute, err := preflightSourcePath(sourcePath)
	if err != nil {
		return SchemaInspection{}, err
	}
	source, err := os.Open(absolute)
	if err != nil {
		return SchemaInspection{}, fmt.Errorf("open preflight source: %w", err)
	}
	defer source.Close()
	sourceInfo, err := source.Stat()
	if err != nil {
		return SchemaInspection{}, fmt.Errorf("inspect preflight source: %w", err)
	}
	pathInfo, err := os.Stat(absolute)
	if err != nil || !os.SameFile(sourceInfo, pathInfo) {
		return SchemaInspection{}, fmt.Errorf("preflight source changed while opening")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		journalPath := absolute + suffix
		if info, statErr := os.Lstat(journalPath); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || info.Size() != 0 {
				return SchemaInspection{}, fmt.Errorf("preflight source has a live SQLite journal")
			}
		} else if !os.IsNotExist(statErr) {
			return SchemaInspection{}, fmt.Errorf("inspect preflight journal: %w", statErr)
		}
	}

	temporary, err := os.CreateTemp(filepath.Dir(absolute), ".helm-preflight-*")
	if err != nil {
		return SchemaInspection{}, fmt.Errorf("create preflight copy: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		_ = os.Remove(temporaryPath + "-wal")
		_ = os.Remove(temporaryPath + "-shm")
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return SchemaInspection{}, fmt.Errorf("secure preflight copy: %w", err)
	}
	if _, err := io.Copy(temporary, source); err != nil {
		return SchemaInspection{}, fmt.Errorf("copy preflight source: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return SchemaInspection{}, fmt.Errorf("sync preflight copy: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return SchemaInspection{}, fmt.Errorf("close preflight copy: %w", err)
	}
	pathInfoAfter, err := os.Stat(absolute)
	if err != nil || !os.SameFile(sourceInfo, pathInfoAfter) {
		return SchemaInspection{}, fmt.Errorf("preflight source changed during copy")
	}
	if pathInfoAfter.Size() != sourceInfo.Size() || pathInfoAfter.ModTime() != sourceInfo.ModTime() {
		return SchemaInspection{}, fmt.Errorf("preflight source contents changed during copy")
	}

	database, err := Open(ctx, temporaryPath)
	if err != nil {
		return SchemaInspection{}, fmt.Errorf("migrate preflight copy: %w", err)
	}
	inspection, inspectErr := InspectSchema(ctx, database)
	if inspectErr == nil {
		inspectErr = CheckIntegrity(ctx, database)
	}
	closeErr := database.Close()
	if inspectErr != nil {
		return SchemaInspection{}, inspectErr
	}
	if closeErr != nil {
		return SchemaInspection{}, fmt.Errorf("close preflight database: %w", closeErr)
	}
	if len(inspection.UnknownVersions) != 0 {
		return SchemaInspection{}, fmt.Errorf("database contains schema newer than this binary")
	}
	if inspection.SchemaVersion != inspection.EmbeddedSchemaVersion || len(inspection.PendingVersions) != 0 {
		return SchemaInspection{}, fmt.Errorf("database schema is not at embedded version")
	}
	return inspection, nil
}

// MigrateCandidate upgrades one already-staged database path in place and
// validates the result. It is intentionally separate from Preflight: restore
// uses it only after copying a retained backup into a private, disposable
// staging directory. Callers must never pass the live production database.
func MigrateCandidate(ctx context.Context, candidatePath string) (SchemaInspection, error) {
	absolute, err := preflightSourcePath(candidatePath)
	if err != nil {
		return SchemaInspection{}, err
	}
	database, err := Open(ctx, absolute)
	if err != nil {
		return SchemaInspection{}, fmt.Errorf("migrate candidate: %w", err)
	}
	inspection, inspectErr := InspectSchema(ctx, database)
	if inspectErr == nil {
		inspectErr = CheckIntegrity(ctx, database)
	}
	closeErr := database.Close()
	if inspectErr != nil {
		return SchemaInspection{}, inspectErr
	}
	if closeErr != nil {
		return SchemaInspection{}, fmt.Errorf("close candidate database: %w", closeErr)
	}
	if len(inspection.UnknownVersions) != 0 {
		return SchemaInspection{}, fmt.Errorf("candidate database contains schema newer than this binary")
	}
	if inspection.SchemaVersion != inspection.EmbeddedSchemaVersion || len(inspection.PendingVersions) != 0 {
		return SchemaInspection{}, fmt.Errorf("candidate database schema is not at embedded version")
	}
	return inspection, nil
}

func preflightSourcePath(sourcePath string) (string, error) {
	if strings.TrimSpace(sourcePath) == "" || sourcePath == ":memory:" || strings.HasPrefix(sourcePath, "file:") {
		return "", fmt.Errorf("preflight source must be a filesystem path")
	}
	absolute, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("resolve preflight source: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect preflight source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("preflight source must be a regular file")
	}
	return absolute, nil
}
