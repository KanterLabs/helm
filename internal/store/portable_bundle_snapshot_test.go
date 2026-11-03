package store

// Failure inventory before the coherent-export fix: a concurrent publication
// between archive and attachment queries can create a dangling link; deletion
// can drop an image from an already captured comment; metadata changes can mix
// generations; the GitHub check can authorize an archive other than the one
// returned; GC can remove captured originals before ZIP assembly. The tests
// interleave real committed writes with the snapshot queries and check the
// returned ZIP and the shared GC lease without timing-dependent sleeps.
import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/KanterLabs/helm/internal/attachments"
	"github.com/KanterLabs/helm/internal/db"
)

type bundleSnapshotSQL struct {
	portableSQL
	beforeImages func()
}

func (q *bundleSnapshotSQL) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if q.beforeImages != nil && strings.Contains(query, "FROM task_attachments a JOIN task_attachment_links") {
		f := q.beforeImages
		q.beforeImages = nil
		f()
	}
	return q.portableSQL.QueryContext(ctx, query, args...)
}
func bundleSnapshotFixture(t *testing.T) (*Store, Actor, Project, Task, string, Attachment, Comment) {
	t.Helper()
	ctx := context.Background()
	database, e := db.Open(ctx, filepath.Join(t.TempDir(), "snapshot.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { database.Close() })
	s := New(database)
	actor, e := s.CreateActor(ctx, Actor{Kind: "human", Name: "Snapshot owner"}, "")
	if e != nil {
		t.Fatal(e)
	}
	project, e := s.CreateProject(ctx, ProjectInput{Key: portableTestString("BSNAP"), Name: portableTestString("Bundle snapshot")}, actor.ID)
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("Snapshot task")}, actor.ID)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	var imageBytes bytes.Buffer
	if e = png.Encode(&imageBytes, image.NewNRGBA(image.Rect(0, 0, 3, 2))); e != nil {
		t.Fatal(e)
	}
	prepared, e := attachments.Prepare(root, bytes.NewReader(imageBytes.Bytes()), "snapshot.png", attachments.DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	defer prepared.Cleanup()
	a, _, e := s.ReserveAttachment(ctx, Attachment{TaskID: task.ID, ProjectID: project.ID, ActorID: actor.ID, Filename: prepared.Filename, ContentType: prepared.ContentType, SizeBytes: prepared.SizeBytes, StoredBytes: prepared.StoredBytes, SHA256: prepared.SHA256, Width: prepared.Width, Height: prepared.Height}, "", "", 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if e = prepared.Install(root, a.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.MarkAttachmentReady(ctx, a.ID); e != nil {
		t.Fatal(e)
	}
	comment, e := s.CreateCommentWithAttachments(ctx, task.ID, actor.ID, "Before snapshot", []string{a.ID}, false)
	if e != nil {
		t.Fatal(e)
	}
	return s, actor, project, task, root, a, comment
}
func TestPortableBundleMetadataUsesArchiveReadSnapshot(t *testing.T) {
	s, actor, project, task, _, a, comment := bundleSnapshotFixture(t)
	ctx := context.Background()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	q := &bundleSnapshotSQL{portableSQL: tx}
	var writerErr error
	q.beforeImages = func() {
		if _, writerErr = s.DB.ExecContext(ctx, `UPDATE comments SET deleted_at=?,body='[deleted]' WHERE id=?`, now(), comment.ID); writerErr != nil {
			return
		}
		_, writerErr = s.CreateComment(ctx, task.ID, actor.ID, "After snapshot")
	}
	archive, manifest, e := exportPortableBundleSnapshot(ctx, q, []string{project.ID})
	if e != nil {
		t.Fatal(e)
	}
	if writerErr != nil {
		t.Fatal(writerErr)
	}
	if q.beforeImages != nil {
		t.Fatal("writer hook did not execute")
	}
	if len(archive.Comments) != 1 || archive.Comments[0].Body != "Before snapshot" {
		t.Fatalf("mixed archive: %+v", archive.Comments)
	}
	if len(manifest.Attachments) != 1 || manifest.Attachments[0].Attachment.ID != a.ID || manifest.Attachments[0].CommentID != comment.ID {
		t.Fatalf("mixed attachment snapshot: %+v", manifest.Attachments)
	}
	var liveComments int
	if e = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE task_id=?`, task.ID).Scan(&liveComments); e != nil || liveComments != 2 {
		t.Fatalf("writer did not commit: %d %v", liveComments, e)
	}
}
func TestPortableBundleCheckedExportPinsAndAuthorizesExactArchive(t *testing.T) {
	s, actor, project, task, root, _, comment := bundleSnapshotFixture(t)
	ctx := context.Background()
	var checked PortableArchive
	body, e := s.ExportPortableBundleChecked(ctx, []string{project.ID}, root, func(archive PortableArchive) error {
		checked = archive
		lock, e := os.OpenFile(filepath.Join(root, ".gc.lock"), os.O_RDWR, 0600)
		if e != nil {
			return e
		}
		defer lock.Close()
		e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			return errors.New("GC shared pin absent during source access check")
		}
		if !errors.Is(e, syscall.EWOULDBLOCK) {
			return e
		}
		if _, e = s.DB.ExecContext(ctx, `UPDATE comments SET deleted_at=?,body='[deleted]' WHERE id=?`, now(), comment.ID); e != nil {
			return e
		}
		_, e = s.CreateComment(ctx, task.ID, actor.ID, "After authorization snapshot")
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	zr, e := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if e != nil {
		t.Fatal(e)
	}
	var got PortableArchive
	var archiveJSON []byte
	var manifest PortableBundleManifest
	for _, f := range zr.File {
		if f.Name != "archive.json" && f.Name != "manifest.json" {
			continue
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		if f.Name == "archive.json" {
			archiveJSON = b
			e = json.Unmarshal(b, &got)
		} else {
			e = json.Unmarshal(b, &manifest)
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	checkedJSON, e := json.Marshal(checked)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(checkedJSON, archiveJSON) {
		t.Fatal("source access checked a different archive from ZIP")
	}
	if len(manifest.Attachments) != 1 {
		t.Fatalf("captured image lost after concurrent deletion: %+v", manifest)
	}
	denied := errors.New("source access denied")
	body, e = s.ExportPortableBundleChecked(ctx, []string{project.ID}, root, func(PortableArchive) error { return denied })
	if !errors.Is(e, denied) || len(body) != 0 {
		t.Fatalf("denied export returned bytes: %d %v", len(body), e)
	}
}
