package store

// Failure inventory: a small JPEG may generate a much larger PNG thumbnail;
// quota must charge both objects before installation, and deleting or staging
// an attachment must not release bytes before physical retention cleanup.
import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/KanterLabs/helm/internal/attachments"
	"github.com/KanterLabs/helm/internal/db"
)

func TestAttachmentQuotaCountsJPEGThumbnailAndRetainedStatuses(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	actor, project, task := createAgentWorkFixture(t, data, ctx, "FOOTPRINT")
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	seed := uint32(1)
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			seed = 1664525*seed + 1013904223
			v := uint8(seed >> 24)
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	var original bytes.Buffer
	if err = jpeg.Encode(&original, img, &jpeg.Options{Quality: 10}); err != nil {
		t.Fatal(err)
	}
	prepared, err := attachments.Prepare(t.TempDir(), bytes.NewReader(original.Bytes()), "capture.jpg", attachments.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	thumb, err := os.Stat(filepath.Join(prepared.Dir, "thumbnail.png"))
	if err != nil {
		t.Fatal(err)
	}
	if thumb.Size() <= prepared.SizeBytes {
		t.Fatalf("fixture needs thumbnail larger than JPEG: original=%d thumbnail=%d", prepared.SizeBytes, thumb.Size())
	}
	if prepared.StoredBytes != prepared.SizeBytes+thumb.Size() {
		t.Fatalf("footprint=%d want original %d + thumbnail %d", prepared.StoredBytes, prepared.SizeBytes, thumb.Size())
	}
	input := Attachment{TaskID: task.ID, ProjectID: project.ID, ActorID: actor.ID, Filename: prepared.Filename, ContentType: prepared.ContentType, SizeBytes: prepared.SizeBytes, StoredBytes: prepared.StoredBytes, SHA256: prepared.SHA256, Width: prepared.Width, Height: prepared.Height}
	if _, _, err = data.ReserveAttachment(ctx, input, "too-small", "hash", prepared.StoredBytes-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("quota admitted JPEG without its PNG thumbnail: %v", err)
	}
	first, _, err := data.ReserveAttachment(ctx, input, "first", "hash", prepared.StoredBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"staged", "deleted"} {
		if _, err = database.ExecContext(ctx, `UPDATE task_attachments SET status=? WHERE id=?`, status, first.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err = data.ReserveAttachment(ctx, input, "overflow-"+status, "hash", prepared.StoredBytes*2-1); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s retained bytes not charged: %v", status, err)
		}
	}
	var recorded int64
	if err = database.QueryRowContext(ctx, `SELECT stored_bytes FROM task_attachments WHERE id=?`, first.ID).Scan(&recorded); err != nil || recorded != prepared.StoredBytes {
		t.Fatalf("recorded footprint=%d error=%v", recorded, err)
	}
}
