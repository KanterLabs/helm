package attachments

// Failure inventory: forged MIME/extension, SVG/HTML, invalid/truncated image,
// animated PNG/WebP, oversized encoded files, oversized dimensions/pixels,
// malformed WebP chunk framing, and decompression concurrency exhaustion.
import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareValidatesAndPreservesOriginal(t *testing.T) {
	var encoded bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 12, 8))
	img.Set(2, 2, color.White)
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	b, err := Prepare(root, bytes.NewReader(encoded.Bytes()), "../../screen.png", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Cleanup()
	original, err := os.ReadFile(filepath.Join(b.Dir, "original"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, encoded.Bytes()) || b.Width != 12 || b.Height != 8 || b.Filename != "screen.png" {
		t.Fatalf("bad result: %+v", b)
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "thumbnail.png")); err != nil {
		t.Fatal(err)
	}
}
func TestPrepareRejectsUnsafeAndOverLimit(t *testing.T) {
	var valid bytes.Buffer
	_ = png.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 10, 10)))
	tests := []struct {
		name   string
		data   []byte
		limits Limits
	}{
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), DefaultLimits()},
		{"html", []byte(`<html>hello</html>`), DefaultLimits()},
		{"truncated", valid.Bytes()[:30], DefaultLimits()},
		{"encoded limit", valid.Bytes(), Limits{MaxFileBytes: 20, MaxPixels: 1000, MaxDimension: 100}},
		{"dimension", valid.Bytes(), Limits{MaxFileBytes: 10000, MaxPixels: 1000, MaxDimension: 9}},
		{"pixels", valid.Bytes(), Limits{MaxFileBytes: 10000, MaxPixels: 99, MaxDimension: 100}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := Prepare(t.TempDir(), bytes.NewReader(tt.data), "image.png", tt.limits)
			if b != nil {
				b.Cleanup()
			}
			if err == nil {
				t.Fatal("accepted unsafe image")
			}
		})
	}
}
