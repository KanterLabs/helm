package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/KanterLabs/helm/internal/attachments"
)

// ImportPortableBundle validates every archive member, original image and link
// before using the normal portable planner. Object installs precede the single
// database commit; a failed transaction removes only objects owned by this run.
// A process interruption can leave unreferenced objects for retention, never
// partially published rows. Replays reuse matching destination records.
func (s *Store) ImportPortableBundle(ctx context.Context, data []byte, root string, options PortableImportOptions, quota int64, configured ...attachments.Limits) (PortableImportReport, error) {
	report := PortableImportReport{Format: PortableBundleFormat, Version: 5, DryRun: options.DryRun, Conflict: options.Conflict, Remaps: []PortableImportRemap{}, Warnings: []string{}, Errors: []PortableImportIssue{}}
	unlock, err := attachments.LockGC(root, false)
	if err != nil {
		return report, err
	}
	defer unlock()
	limits := attachments.DefaultLimits()
	if len(configured) > 0 {
		limits = configured[0]
	}
	archive, manifest, prepared, err := decodePortableBundle(data, root, limits)
	if err != nil {
		return report, err
	}
	defer func() {
		for _, p := range prepared {
			p.Cleanup()
		}
	}()
	options.bundleComments = map[string]bool{}
	for _, entry := range manifest.Attachments {
		if entry.CommentID != "" {
			options.bundleComments[entry.CommentID] = true
		}
	}
	archive.normalize()
	if options.Conflict == "" {
		options.Conflict = portableConflictRemap
	}
	finish := func(plan portableImportPlan) {
		if plan.report.Format != "" {
			report = plan.report
		}
		report.Format = PortableBundleFormat
		report.Version = 5
	}
	if options.DryRun {
		plan, e := buildPortableImportPlan(ctx, s.DB, archive, options)
		if e == nil {
			_, e = planPortableBundle(ctx, s.DB, root, manifest, prepared, &plan, quota)
		}
		finish(plan)
		return report, bundleReportError(e, report)
	}
	installed := []string{}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		plan, e := buildPortableImportPlan(ctx, tx, archive, options)
		if e != nil {
			finish(plan)
			return e
		}
		bundle, e := planPortableBundle(ctx, tx, root, manifest, prepared, &plan, quota)
		finish(plan)
		if e != nil {
			return e
		}
		if e = executePortableImportPlan(ctx, tx, &plan); e != nil {
			return e
		}
		for _, record := range bundle {
			if !record.create {
				continue
			}
			v := record.entry
			a := v.Attachment
			if e = record.prepared.Install(root, a.ID); e != nil {
				return e
			}
			installed = append(installed, a.ID)
			_, e = tx.ExecContext(ctx, `INSERT INTO task_attachments(id,task_id,project_id,actor_id,filename,content_type,size_bytes,sha256,width,height,status,caption,commit_sha,build,viewport_width,viewport_height,version,created_at,updated_at,stored_bytes,ready) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)`, a.ID, a.TaskID, a.ProjectID, a.ActorID, a.Filename, a.ContentType, a.SizeBytes, a.SHA256, a.Width, a.Height, a.Status, a.Caption, a.CommitSHA, a.Build, a.ViewportWidth, a.ViewportHeight, a.Version, a.CreatedAt, a.UpdatedAt, a.StoredBytes)
			if e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, `INSERT INTO task_attachment_links(attachment_id,comment_id,progress_id,created_at,position) VALUES(?,NULLIF(?,''),NULLIF(?,''),?,?)`, a.ID, v.CommentID, v.ProgressID, v.LinkedAt, v.Position)
			if e != nil {
				return e
			}
		}
		finish(plan)
		return nil
	})
	if err != nil {
		for _, id := range installed {
			_ = attachments.RemoveObject(root, id)
		}
	}
	return report, bundleReportError(err, report)
}

func bundleReportError(err error, report PortableImportReport) error {
	var typed *Error
	if errors.As(err, &typed) {
		if _, ok := typed.Details.(PortableImportReport); ok {
			typed.Details = report
		}
	}
	return err
}
