package betaswitch

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
)

// binaryStamp identifies one verified binary and its checksum record by
// filesystem identity. Retained releases are immutable, root-owned
// directories, so an unchanged device, inode, size, mtime, and ctime for both
// files means the bytes that were hashed have not been replaced. Any rewrite,
// rename, chmod, or truncation changes ctime and forces a full re-hash.
type binaryStamp struct {
	binary   fileIdentity
	checksum fileIdentity
}

type fileIdentity struct {
	dev, ino     uint64
	size         int64
	mtime, ctime syscall.Timespec
}

func identityOf(path string) (fileIdentity, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fileIdentity{}, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, false
	}
	return fileIdentity{dev: uint64(stat.Dev), ino: stat.Ino, size: stat.Size, mtime: stat.Mtim, ctime: stat.Ctim}, true
}

func stampOf(dir, name string) (binaryStamp, bool) {
	binary, ok := identityOf(filepath.Join(dir, name))
	if !ok {
		return binaryStamp{}, false
	}
	checksum, ok := identityOf(filepath.Join(dir, name+".sha256"))
	if !ok {
		return binaryStamp{}, false
	}
	return binaryStamp{binary: binary, checksum: checksum}, true
}

// validateBinaryCached is used only for release listings. Hashing every
// retained Helm and Codex binary on each request grows with the number of
// retained releases (hundreds of MiB each) and made /v1/releases slower than
// the installer's readiness probe. A switch still calls findRelease, which
// always performs the full validateBinary check.
func (b *Broker) validateBinaryCached(dir, name string) error {
	path := filepath.Join(dir, name)
	before, stamped := stampOf(dir, name)
	if stamped {
		b.binaryMu.Lock()
		cached, ok := b.binaryCache[path]
		b.binaryMu.Unlock()
		if ok && cached == before {
			return nil
		}
	}
	if err := validateBinary(dir, name); err != nil {
		b.binaryMu.Lock()
		delete(b.binaryCache, path)
		b.binaryMu.Unlock()
		return err
	}
	after, ok := stampOf(dir, name)
	if !stamped || !ok || after != before {
		// The files changed while they were being verified; do not cache.
		return nil
	}
	b.binaryMu.Lock()
	if b.binaryCache == nil {
		b.binaryCache = make(map[string]binaryStamp)
	}
	b.binaryCache[path] = after
	b.binaryMu.Unlock()
	return nil
}

// WarmReleaseCache verifies retained releases once so the first listing after
// a daemon restart is fast. The daemon runs it in the background alongside
// Serve; errors are ignored here because ListReleases reports them on demand.
func (b *Broker) WarmReleaseCache(ctx context.Context) {
	_, _ = b.ListReleases(ctx)
}
