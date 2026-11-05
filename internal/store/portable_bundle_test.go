package store

// Failure inventory, written before implementation: invalid ZIP names/types,
// duplicate members, oversized expansion, missing/extra blobs, mismatched size
// or hash, invalid/huge encoded images, dangling/cross-task publication links,
// attachment ID conflicts, quota exhaustion, dry-run mutation, partial DB
// writes, destination overwrite, and replay duplication must all be rejected
// or safely remapped before publication. This integration round trip exercises
// real SQLite plans, publication links, object installation and replay.
import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/KanterLabs/helm/internal/db"
)

func TestPortableBundleRoundTripAndCorruption(t *testing.T) {
	ctx := context.Background()
	open := func(name string) *Store {
		d, e := db.Open(ctx, filepath.Join(t.TempDir(), name+".db"))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { d.Close() })
		return New(d)
	}
	source := open("source")
	dest := open("dest")
	importer, err := dest.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	actor, e := source.CreateActor(ctx, Actor{Kind: "human", Name: "Owner"}, "")
	if e != nil {
		t.Fatal(e)
	}
	project, e := source.CreateProject(ctx, ProjectInput{Key: portableTestString("BUNDLE"), Name: portableTestString("Bundle")}, actor.ID)
	if e != nil {
		t.Fatal(e)
	}
	task, e := source.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("With image")}, actor.ID)
	if e != nil {
		t.Fatal(e)
	}
	var original bytes.Buffer
	if e = png.Encode(&original, image.NewNRGBA(image.Rect(0, 0, 3, 2))); e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(original.Bytes())
	a, _, e := source.ReserveAttachment(ctx, Attachment{TaskID: task.ID, ProjectID: project.ID, ActorID: actor.ID, Filename: "proof.png", ContentType: "image/png", SizeBytes: int64(original.Len()), SHA256: hex.EncodeToString(hash[:]), Width: 3, Height: 2}, "", "", 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	object := filepath.Join(root, "objects", a.ID)
	if e = os.MkdirAll(object, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(object, "original"), original.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	if e = source.MarkAttachmentReady(ctx, a.ID); e != nil {
		t.Fatal(e)
	}
	comment, e := source.CreateCommentWithAttachments(ctx, task.ID, actor.ID, "Proof", []string{a.ID}, false)
	if e != nil {
		t.Fatal(e)
	}
	bundle, e := source.ExportPortableBundle(ctx, []string{project.ID}, root)
	if e != nil {
		t.Fatal(e)
	}
	destinationRoot := t.TempDir()
	preview, e := dest.ImportPortableBundle(ctx, bundle, destinationRoot, PortableImportOptions{DryRun: true, ActorID: importer.ID}, 1<<20)
	if e != nil {
		t.Fatalf("preview: %v report=%+v", e, preview)
	}
	if !preview.DryRun {
		t.Fatal("preview mutated")
	}
	report, e := dest.ImportPortableBundle(ctx, bundle, destinationRoot, PortableImportOptions{ActorID: importer.ID}, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if report.Version != 5 {
		t.Fatalf("report=%+v", report)
	}
	got, e := dest.AttachmentsForComment(ctx, comment.ID)
	if e != nil || len(got) != 1 {
		t.Fatalf("links=%+v err=%v", got, e)
	}
	saved, e := os.ReadFile(filepath.Join(destinationRoot, "objects", got[0].ID, "original"))
	if e != nil || !bytes.Equal(saved, original.Bytes()) {
		t.Fatalf("original changed: %v", e)
	}
	if _, e = os.Stat(filepath.Join(destinationRoot, "objects", got[0].ID, "thumbnail.png")); e != nil {
		t.Fatal(e)
	}
	if _, e = dest.ImportPortableBundle(ctx, bundle, destinationRoot, PortableImportOptions{ActorID: importer.ID}, 1<<20); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = dest.DB.QueryRow(`SELECT COUNT(*) FROM task_attachments`).Scan(&count); e != nil || count != 1 {
		t.Fatalf("replay count=%d err=%v", count, e)
	}
	zr, e := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if e != nil {
		t.Fatal(e)
	}
	var broken bytes.Buffer
	zw := zip.NewWriter(&broken)
	for _, f := range zr.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		var b bytes.Buffer
		b.ReadFrom(r)
		r.Close()
		w, e := zw.Create(f.Name)
		if e != nil {
			t.Fatal(e)
		}
		if f.Name == "objects/"+a.ID+"/original" {
			w.Write([]byte("corrupt"))
		} else {
			w.Write(b.Bytes())
		}
	}
	if e = zw.Close(); e != nil {
		t.Fatal(e)
	}
	before, _ := dest.ExportPortable(ctx, nil)
	if _, e = dest.ImportPortableBundle(ctx, broken.Bytes(), destinationRoot, PortableImportOptions{ActorID: importer.ID}, 1<<20); e == nil {
		t.Fatal("corruption accepted")
	}
	after, _ := dest.ExportPortable(ctx, nil)
	if len(before.Tasks) != len(after.Tasks) || len(before.Comments) != len(after.Comments) {
		t.Fatal("corrupt import mutated destination")
	}
}
