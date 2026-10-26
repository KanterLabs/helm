package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

func archiveTaskByID(tasks []PortableTask, id string) (PortableTask, bool) {
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return PortableTask{}, false
}

func portableStringArg(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func portableMarshalRefs(refs []string) (string, error) {
	if refs == nil {
		refs = []string{}
	}
	encoded, err := json.Marshal(refs)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// ValidatePortable performs all archive and destination checks without opening
// a write transaction or changing any row. The returned report is useful for
// a UI preview and is also embedded in validation errors by the HTTP layer.
func (s *Store) ValidatePortable(ctx context.Context, archive PortableArchive, options PortableImportOptions) (PortableImportReport, error) {
	options.DryRun = true
	archive.normalize()
	plan, err := buildPortableImportPlan(ctx, s.DB, archive, options)
	if plan.report.Format == "" {
		plan.report = PortableImportReport{Format: archive.Format, Version: archive.Version, DryRun: true, Conflict: options.Conflict, Remaps: []PortableImportRemap{}, Warnings: []string{}, Errors: []PortableImportIssue{}}
	}
	return plan.report, err
}

// ImportPortable inserts a validated archive in one transaction. Existing
// rows are reused only when their stable identifiers and exported fields are
// equal; otherwise IDs/keys/numbers are deterministically remapped and every
// remap is returned. It never updates, deletes, or replaces destination data.
func (s *Store) ImportPortable(ctx context.Context, archive PortableArchive, options PortableImportOptions) (PortableImportReport, error) {
	if options.Conflict == "" {
		options.Conflict = portableConflictRemap
	}
	if options.DryRun {
		return s.ValidatePortable(ctx, archive, options)
	}
	archive.normalize()
	report := PortableImportReport{Format: archive.Format, Version: archive.Version, Conflict: options.Conflict, Remaps: []PortableImportRemap{}, Warnings: []string{}, Errors: []PortableImportIssue{}}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		plan, planErr := buildPortableImportPlan(ctx, tx, archive, options)
		report = plan.report
		if planErr != nil {
			return planErr
		}
		executeErr := executePortableImportPlan(ctx, tx, &plan)
		report = plan.report
		return executeErr
	})
	return report, err
}
