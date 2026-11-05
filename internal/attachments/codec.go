// Package attachments validates private raster evidence and owns its disk layout.
package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type Limits struct {
	MaxFileBytes       int64
	MaxPixels          int64
	MaxDimension       int
	MaxImagesPerUpdate int
	ProjectQuotaBytes  int64
}

func DefaultLimits() Limits { return Limits{10 << 20, 40_000_000, 8192, 4, 1 << 30} }

var decodeSlots = make(chan struct{}, 2)
var ErrInvalid = errors.New("invalid image: provide a static PNG, JPEG, or WebP within the configured limits")
var ErrTooLarge = errors.New("image exceeds the maximum file size")
var ErrBusy = errors.New("image processing is busy; retry later")

type Prepared struct {
	Dir, Filename, ContentType, SHA256 string
	SizeBytes                          int64
	StoredBytes                        int64
	Width, Height                      int
}

func (p *Prepared) Cleanup() {
	if p != nil && p.Dir != "" {
		_ = os.RemoveAll(p.Dir)
	}
}
func SanitizeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		name = "screenshot"
	}
	if len(name) > 180 {
		name = name[:180]
	}
	return name
}

// Prepare streams the original to a private staging directory. Decode concurrency
// and dimensions are bounded before allocating decoded raster memory.
func Prepare(root string, r io.Reader, name string, limits Limits) (*Prepared, error) {
	select {
	case decodeSlots <- struct{}{}:
		defer func() { <-decodeSlots }()
	default:
		return nil, ErrBusy
	}
	if err := EnsureRoot(root); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(filepath.Join(root, "staging"), "upload-")
	if err != nil {
		return nil, err
	}
	p := &Prepared{Dir: dir, Filename: SanitizeFilename(name)}
	ok := false
	defer func() {
		if !ok {
			p.Cleanup()
		}
	}()
	f, err := os.OpenFile(filepath.Join(dir, "original"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limits.MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if n > limits.MaxFileBytes {
		return nil, ErrTooLarge
	}
	if n == 0 {
		return nil, ErrInvalid
	}
	p.SizeBytes = n
	p.SHA256 = hex.EncodeToString(h.Sum(nil))
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	cfg, format, err := image.DecodeConfig(f)
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > limits.MaxDimension || cfg.Height > limits.MaxDimension || int64(cfg.Width)*int64(cfg.Height) > limits.MaxPixels {
		return nil, ErrInvalid
	}
	switch format {
	case "png":
		p.ContentType = "image/png"
	case "jpeg":
		p.ContentType = "image/jpeg"
	case "webp":
		p.ContentType = "image/webp"
	default:
		return nil, ErrInvalid
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	if err = rejectAnimation(f, format, n); err != nil {
		return nil, err
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, ErrInvalid
	}
	p.Width, p.Height = cfg.Width, cfg.Height
	width, height := cfg.Width, cfg.Height
	if width > 480 || height > 320 {
		scale := min(480.0/float64(width), 320.0/float64(height))
		width = max(1, int(float64(width)*scale))
		height = max(1, int(float64(height)*scale))
	}
	thumb := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(thumb, thumb.Bounds(), img, img.Bounds(), draw.Src, nil)
	out, err := os.OpenFile(filepath.Join(dir, "thumbnail.png"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	err = png.Encode(out, thumb)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	thumbInfo, err := os.Stat(filepath.Join(dir, "thumbnail.png"))
	if err != nil {
		return nil, err
	}
	p.StoredBytes = p.SizeBytes + thumbInfo.Size()
	ok = true
	return p, nil
}
func rejectAnimation(f *os.File, format string, size int64) error {
	if format == "jpeg" {
		return nil
	}
	if format == "png" {
		if _, err := f.Seek(8, 0); err != nil {
			return err
		}
		for {
			var hdr [8]byte
			if _, err := io.ReadFull(f, hdr[:]); err != nil {
				return ErrInvalid
			}
			n := int64(binary.BigEndian.Uint32(hdr[:4]))
			kind := string(hdr[4:])
			if kind == "acTL" || kind == "fcTL" || kind == "fdAT" {
				return ErrInvalid
			}
			pos, _ := f.Seek(0, io.SeekCurrent)
			if n > size-pos-4 {
				return ErrInvalid
			}
			if _, err := f.Seek(n+4, io.SeekCurrent); err != nil {
				return ErrInvalid
			}
			if kind == "IEND" {
				return nil
			}
		}
	}
	var head [12]byte
	if _, err := io.ReadFull(f, head[:]); err != nil || !bytes.Equal(head[:4], []byte("RIFF")) || string(head[8:]) != "WEBP" || int64(binary.LittleEndian.Uint32(head[4:8]))+8 != size {
		return ErrInvalid
	}
	for {
		pos, _ := f.Seek(0, io.SeekCurrent)
		if pos == size {
			return nil
		}
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return ErrInvalid
		}
		n := int64(binary.LittleEndian.Uint32(hdr[4:]))
		kind := string(hdr[:4])
		if kind == "ANIM" || kind == "ANMF" {
			return ErrInvalid
		}
		if n > size-pos-8 {
			return ErrInvalid
		}
		if kind == "VP8X" {
			var flags [1]byte
			if n < 1 {
				return ErrInvalid
			}
			if _, err := io.ReadFull(f, flags[:]); err != nil || flags[0]&2 != 0 {
				return ErrInvalid
			}
			n--
		}
		if _, err := f.Seek(n+(int64(binary.LittleEndian.Uint32(hdr[4:]))&1), io.SeekCurrent); err != nil {
			return ErrInvalid
		}
	}
}
