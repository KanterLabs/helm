package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"

	"github.com/KanterLabs/helm/internal/attachments"
)

// ExportPortableBundle includes only objects whose task and canonical update
// exist in one read snapshot. Missing/corrupt originals fail closed.
func (s *Store) ExportPortableBundle(ctx context.Context, projectIDs []string, root string) ([]byte, error) {
	return s.ExportPortableBundleChecked(ctx, projectIDs, root, nil)
}

// ExportPortableBundleChecked pins immutable objects against retention, captures
// the JSON archive and image metadata in one transaction, and invokes check on
// that exact archive before assembling bytes. check must treat archive as read-only.
func (s *Store) ExportPortableBundleChecked(ctx context.Context, projectIDs []string, root string, check func(PortableArchive) error) ([]byte, error) {
	unlock, err := attachments.LockGC(root, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	archive, manifest, err := exportPortableBundleSnapshot(ctx, tx, projectIDs)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if check != nil {
		if err = check(archive); err != nil {
			return nil, err
		}
	}
	return encodePortableBundle(archive, manifest, root)
}

// ExportPortableBundleArchive retains the caller-supplied archive API, while
// refusing to combine a stale JSON snapshot with newer image metadata. Callers
// that need an access check should use ExportPortableBundleChecked directly.
func (s *Store) ExportPortableBundleArchive(ctx context.Context, archive PortableArchive, root string) ([]byte, error) {
	projects := make([]string, 0, len(archive.Projects))
	for _, project := range archive.Projects {
		projects = append(projects, project.ID)
	}
	return s.ExportPortableBundleChecked(ctx, projects, root, func(current PortableArchive) error {
		archive.ExportedAt = ""
		current.ExportedAt = ""
		if !reflect.DeepEqual(archive, current) {
			return conflict("portable archive changed before bundle export; retry with a fresh snapshot", nil)
		}
		return nil
	})
}

func exportPortableBundleSnapshot(ctx context.Context, q portableSQL, projectIDs []string) (PortableArchive, PortableBundleManifest, error) {
	archive, e := exportPortableVersionSnapshot(ctx, q, projectIDs, PortableVersionCurrent)
	if e != nil {
		return archive, PortableBundleManifest{}, e
	}
	taskSet := map[string]bool{}
	comments := map[string]bool{}
	history := map[string]bool{}
	for _, t := range archive.Tasks {
		taskSet[t.ID] = true
	}
	for _, c := range archive.Comments {
		comments[c.ID] = true
	}
	for _, h := range archive.Activity.AgentWorkHistory {
		history[h.ID] = true
	}
	manifest := PortableBundleManifest{Format: PortableBundleFormat, Version: 5, Archive: "archive.json", Attachments: []PortableBundleAttachment{}}
	rows, e := q.QueryContext(ctx, `SELECT `+attachmentColumns+`,COALESCE(l.comment_id,''),COALESCE(l.progress_id,''),l.created_at,l.position FROM task_attachments a JOIN task_attachment_links l ON l.attachment_id=a.id WHERE a.status='published' AND `+visibleAttachment+` ORDER BY a.id`)
	if e != nil {
		return archive, manifest, e
	}
	for rows.Next() {
		var v PortableBundleAttachment
		a := &v.Attachment
		e = rows.Scan(&a.ID, &a.TaskID, &a.ProjectID, &a.ActorID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.Width, &a.Height, &a.Status, &a.Caption, &a.CommitSHA, &a.Build, &a.ViewportWidth, &a.ViewportHeight, &a.Version, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt, &v.CommentID, &v.ProgressID, &v.LinkedAt, &v.Position)
		if e != nil {
			rows.Close()
			return archive, manifest, e
		}
		if !taskSet[a.TaskID] {
			continue
		}
		if (v.CommentID != "" && !comments[v.CommentID]) || (v.ProgressID != "" && !history[v.ProgressID]) {
			rows.Close()
			return archive, manifest, bundleInvalid("published attachment update is absent from snapshot")
		}
		v.Object = "objects/" + a.ID + "/original"
		manifest.Attachments = append(manifest.Attachments, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return archive, manifest, e
	}
	if len(manifest.Attachments) > 1000 {
		return archive, manifest, bundleInvalid("too many attachments")
	}
	archive.Warnings = nil
	return archive, manifest, nil
}

func encodePortableBundle(archive PortableArchive, manifest PortableBundleManifest, root string) ([]byte, error) {
	raw, e := json.Marshal(archive)
	if e != nil {
		return nil, e
	}
	if int64(len(raw)) > portableBundleMaxJSON {
		return nil, bundleInvalid("JSON exceeds size limit")
	}
	manifest.ArchiveSHA256 = bundleHash(raw)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	add := func(name string, b []byte) error {
		w, e := zw.Create(name)
		if e != nil {
			return e
		}
		_, e = w.Write(b)
		return e
	}
	if e = add("archive.json", raw); e != nil {
		return nil, e
	}
	var total int64 = int64(len(raw))
	for _, entry := range manifest.Attachments {
		a := entry.Attachment
		path, e := attachments.ObjectPath(root, a.ID, false)
		if e != nil {
			return nil, e
		}
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() {
			return nil, bundleInvalid("attachment original is unavailable")
		}
		if info.Size() != a.SizeBytes || a.SizeBytes > PortableBundleMaxBytes {
			return nil, bundleInvalid("attachment original size mismatch")
		}
		f, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(io.LimitReader(f, a.SizeBytes+1))
		f.Close()
		if e != nil || int64(len(b)) != a.SizeBytes || bundleHash(b) != a.SHA256 {
			return nil, bundleInvalid("attachment original checksum mismatch")
		}
		total += a.SizeBytes
		if total > PortableBundleMaxBytes {
			return nil, bundleInvalid("expanded archive exceeds size limit")
		}
		if e = add(entry.Object, b); e != nil {
			return nil, e
		}
	}
	raw, e = json.Marshal(manifest)
	if e != nil {
		return nil, e
	}
	if e = add("manifest.json", raw); e != nil {
		return nil, e
	}
	if e = zw.Close(); e != nil {
		return nil, e
	}
	if int64(out.Len()) > PortableBundleMaxBytes {
		return nil, bundleInvalid("encoded archive exceeds size limit")
	}
	return out.Bytes(), nil
}
