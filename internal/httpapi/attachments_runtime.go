package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/KanterLabs/helm/internal/attachments"
)

// startAttachmentRuntime owns recovery and retention independently of web usage.
// Closing the database ends this worker; no storage root or stable lease file is
// ever deleted. Published evidence is retained without a time limit.
func (s *Server) startAttachmentRuntime() {
	if s.Cfg.DB == "" || s.Cfg.DB == ":memory:" {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			if err := s.Store.DB.PingContext(ctx); err != nil {
				cancel()
				return
			}
			s.reconcileAttachments(ctx)
			cancel()
			<-ticker.C
		}
	}()
}
func (s *Server) reconcileAttachments(ctx context.Context) {
	root := s.attachmentRoot()
	unlock, err := attachments.LockGC(root, true)
	if err != nil {
		return
	}
	defer unlock()
	records, err := s.Store.AttachmentRetentionRecords(ctx)
	if err != nil {
		return
	}
	known := map[string]bool{}
	at := time.Now().UTC()
	for _, a := range records {
		known[a.ID] = true
		created, err := time.Parse(time.RFC3339Nano, a.CreatedAt)
		if err != nil {
			continue
		}
		purge := a.Status == "staged" && created.Before(at.Add(-24*time.Hour))
		if a.Status == "deleted" && a.DeletedAt != nil {
			deleted, e := time.Parse(time.RFC3339Nano, *a.DeletedAt)
			purge = e == nil && deleted.Before(at.Add(-30*24*time.Hour))
		}
		if purge {
			_ = s.Store.PurgeAttachmentWithBlob(ctx, a.ID, at, func() error { return attachments.RemoveObject(root, a.ID) })
			continue
		}
		original, e := attachments.ObjectPath(root, a.ID, false)
		if e != nil {
			continue
		}
		thumbnail, _ := attachments.ObjectPath(root, a.ID, true)
		f, e := os.Open(original)
		present := e == nil
		if present {
			info, e := f.Stat()
			present = e == nil && info.Mode().IsRegular() && info.Size() == a.SizeBytes
			if present && !a.Ready {
				hash := sha256.New()
				_, e = io.Copy(hash, f)
				present = e == nil && hex.EncodeToString(hash.Sum(nil)) == a.SHA256
			}
			_ = f.Close()
		}
		if _, e = os.Stat(thumbnail); e != nil {
			present = false
		}
		if present != a.Ready {
			_ = s.Store.SetAttachmentReadiness(ctx, a.ID, present)
		}
	}
	for _, kind := range []string{"staging", "objects"} {
		entries, e := os.ReadDir(filepath.Join(root, kind))
		if e != nil {
			continue
		}
		for _, entry := range entries {
			if kind == "objects" && known[entry.Name()] {
				continue
			}
			info, e := entry.Info()
			if e == nil && info.ModTime().Before(at.Add(-24*time.Hour)) {
				_ = os.RemoveAll(filepath.Join(root, kind, entry.Name()))
			}
		}
	}
}
