package store

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/KanterLabs/helm/internal/attachments"
)

func bundleInvalid(message string) error {
	return &Error{Kind: ErrInvalid, Message: "invalid portable bundle: " + message}
}
func bundleHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func bundleJSON(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return bundleInvalid(err.Error())
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return bundleInvalid("multiple JSON values")
	}
	return nil
}
func bundleRead(f *zip.File, limit int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, bundleInvalid("archive member exceeds size limit")
	}
	r, e := f.Open()
	if e != nil {
		return nil, bundleInvalid("unreadable archive member")
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, bundleInvalid("unreadable or oversized archive member")
	}
	return b, nil
}

// decodePortableBundle never extracts archive names to disk. Exact member
// allowlisting rejects traversal, special files, duplicates and hidden payloads.
func decodePortableBundle(data []byte, root string, limits attachments.Limits) (PortableArchive, PortableBundleManifest, []*attachments.Prepared, error) {
	var archive PortableArchive
	var manifest PortableBundleManifest
	fail := func(e error) (PortableArchive, PortableBundleManifest, []*attachments.Prepared, error) {
		return archive, manifest, nil, e
	}
	if int64(len(data)) > PortableBundleMaxBytes {
		return fail(bundleInvalid("encoded archive exceeds size limit"))
	}
	zr, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return fail(bundleInvalid("ZIP required"))
	}
	if len(zr.File) > portableBundleMaxFiles {
		return fail(bundleInvalid("too many archive members"))
	}
	files := map[string]*zip.File{}
	var size uint64
	for _, f := range zr.File {
		if f.Name == "" || strings.ContainsAny(f.Name, "\\\x00") || f.Mode()&os.ModeType != 0 || files[f.Name] != nil {
			return fail(bundleInvalid("invalid or duplicate member"))
		}
		if f.UncompressedSize64 > uint64(PortableBundleMaxBytes) {
			return fail(bundleInvalid("archive member exceeds size limit"))
		}
		size += f.UncompressedSize64
		if size > uint64(PortableBundleMaxBytes) {
			return fail(bundleInvalid("expanded archive exceeds size limit"))
		}
		files[f.Name] = f
	}
	mf := files["manifest.json"]
	af := files["archive.json"]
	if mf == nil || af == nil {
		return fail(bundleInvalid("manifest.json and archive.json are required"))
	}
	b, e := bundleRead(mf, portableBundleMaxJSON)
	if e != nil {
		return fail(e)
	}
	if e = bundleJSON(b, &manifest); e != nil {
		return fail(e)
	}
	if manifest.Format != PortableBundleFormat || manifest.Version != 5 || manifest.Archive != "archive.json" || len(manifest.Attachments) > 1000 {
		return fail(bundleInvalid("unsupported manifest"))
	}
	b, e = bundleRead(af, portableBundleMaxJSON)
	if e != nil {
		return fail(e)
	}
	if bundleHash(b) != manifest.ArchiveSHA256 {
		return fail(bundleInvalid("archive checksum mismatch"))
	}
	if e = bundleJSON(b, &archive); e != nil {
		return fail(e)
	}
	if archive.Version != 4 {
		return fail(bundleInvalid("bundle requires portable v4 JSON"))
	}
	if len(files) != len(manifest.Attachments)+2 {
		return fail(bundleInvalid("unexpected archive members"))
	}
	prepared := []*attachments.Prepared{}
	ok := false
	defer func() {
		if !ok {
			for _, p := range prepared {
				p.Cleanup()
			}
		}
	}()
	seen := map[string]bool{}
	for _, entry := range manifest.Attachments {
		a := entry.Attachment
		if _, e = attachments.ObjectPath(root, a.ID, false); e != nil {
			return fail(bundleInvalid("invalid attachment ID"))
		}
		expected := "objects/" + a.ID + "/original"
		if entry.Object != expected || seen[a.ID] || files[expected] == nil {
			return fail(bundleInvalid("missing or duplicate attachment object"))
		}
		seen[a.ID] = true
		if entry.Position < 0 || entry.Position >= 4 {
			return fail(bundleInvalid("invalid image position"))
		}
		if a.Status != "published" || a.DeletedAt != nil || a.SizeBytes <= 0 || a.SizeBytes > limits.MaxFileBytes || a.Version < 1 || a.Filename != attachments.SanitizeFilename(a.Filename) || len(a.Caption) > 2000 || len(a.CommitSHA) > 128 || len(a.Build) > 200 || strings.ContainsAny(a.CommitSHA+a.Build, "\r\n\x00") || a.ViewportWidth < 0 || a.ViewportHeight < 0 || a.ViewportWidth > 100000 || a.ViewportHeight > 100000 {
			return fail(bundleInvalid("invalid attachment metadata"))
		}
		b, e := bundleRead(files[expected], limits.MaxFileBytes)
		if e != nil {
			return fail(e)
		}
		if int64(len(b)) != a.SizeBytes || bundleHash(b) != a.SHA256 {
			return fail(bundleInvalid("attachment checksum or size mismatch"))
		}
		p, e := attachments.Prepare(root, bytes.NewReader(b), a.Filename, limits)
		if e != nil {
			return fail(bundleInvalid(fmt.Sprintf("attachment image validation failed: %v", e)))
		}
		prepared = append(prepared, p)
		if p.ContentType != a.ContentType || p.Width != a.Width || p.Height != a.Height {
			return fail(bundleInvalid("attachment encoding metadata mismatch"))
		}
	}
	ok = true
	return archive, manifest, prepared, nil
}
