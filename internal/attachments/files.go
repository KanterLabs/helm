package attachments

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var opaqueID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func EnsureRoot(root string) error {
	for _, dir := range []string{root, filepath.Join(root, "objects"), filepath.Join(root, "staging")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(filepath.Join(root, ".gc.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}

// LockGC is shared for backup and upload/reads, exclusive for retention cleanup.
// The stable lock file and storage root must never be removed during runtime.
func LockGC(root string, exclusive bool) (func(), error) {
	if err := EnsureRoot(root); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, ".gc.lock"), os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err = syscall.Flock(int(f.Fd()), mode); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
func ObjectPath(root, id string, thumbnail bool) (string, error) {
	if !opaqueID.MatchString(id) {
		return "", fmt.Errorf("invalid attachment ID")
	}
	name := "original"
	if thumbnail {
		name = "thumbnail.png"
	}
	return filepath.Join(root, "objects", id, name), nil
}
func (p *Prepared) Install(root, id string) error {
	path, err := ObjectPath(root, id, false)
	if err != nil {
		return err
	}
	if err = os.Rename(p.Dir, filepath.Dir(path)); err != nil {
		return err
	}
	p.Dir = ""
	for _, dir := range []string{filepath.Dir(filepath.Dir(path)), filepath.Join(root, "staging")} {
		f, e := os.Open(dir)
		if e != nil {
			return e
		}
		e = f.Sync()
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
func RemoveObject(root, id string) error {
	path, err := ObjectPath(root, id, false)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(path))
}
