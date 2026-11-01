//go:build linux

package deployadmission

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// runtimeChecks is deliberately private. Production observations cannot be
// configured through the API or environment; tests isolate kernel observations.
type runtimeChecks struct {
	dataPath, authorityPath string
	credentials             func() (processCredentials, error)
	stat                    func(int) (unix.Stat_t, error)
	readOnly                func(int) (bool, error)
	executableHash          func() (string, error)
	afterLock               func()
}

// Acquire returns no lease for public startup when protected data is absent.
// If protected data exists, every authority and process check must succeed.
func Acquire() (*Lease, error) {
	return acquire(runtimeChecks{dataPath: protectedData, authorityPath: authorityDir,
		credentials: currentCredentials, stat: descriptorStat, readOnly: filesystemReadOnly,
		executableHash: runningExecutableHash})
}

func acquire(c runtimeChecks) (*Lease, error) {
	if _, err := os.Lstat(c.dataPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, denied("data unavailable")
	}
	p, err := c.credentials()
	if err != nil || !p.valid() {
		return nil, denied("process credentials")
	}
	data, err := openPath(c.dataPath, true)
	if err != nil {
		return nil, denied("data unavailable")
	}
	defer data.Close()
	ds, err := checkedStat(c, data, unix.S_IFDIR, privateID, 0700)
	if err != nil {
		return nil, err
	}
	database, err := openAt(data, "roadmap.db", false)
	if err != nil {
		return nil, denied("database unavailable")
	}
	_, err = checkedStat(c, database, unix.S_IFREG, privateID, 0600)
	database.Close()
	if err != nil {
		return nil, err
	}
	authority, err := openPath(c.authorityPath, true)
	if err != nil {
		return nil, denied("authority unavailable")
	}
	defer authority.Close()
	if _, err = checkedStat(c, authority, unix.S_IFDIR, 0, 0750); err != nil {
		return nil, err
	}
	readonly, err := c.readOnly(int(authority.Fd()))
	if err != nil || !readonly {
		return nil, denied("authority filesystem")
	}
	lock, err := openAt(authority, "runtime.lock", false)
	if err != nil {
		return nil, denied("lock unavailable")
	}
	keep := false
	defer func() {
		if !keep {
			lock.Close()
		}
	}()
	ls, err := checkedStat(c, lock, unix.S_IFREG, 0, 0640)
	if err != nil {
		return nil, err
	}
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return nil, denied("lock unavailable")
	}
	if c.afterLock != nil {
		c.afterLock()
	}
	if !sameLock(c, authority, ls) {
		return nil, denied("lock identity")
	}
	markerFile, err := openAt(authority, "marker.json", false)
	if err != nil {
		return nil, denied("marker unavailable")
	}
	defer markerFile.Close()
	if _, err = checkedStat(c, markerFile, unix.S_IFREG, 0, 0640); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(markerFile, maxMarkerBytes+1))
	if err != nil {
		return nil, denied("marker unavailable")
	}
	m, err := parseMarker(body)
	if err != nil {
		return nil, err
	}
	if m.DataDevice != uint64(ds.Dev) || m.DataInode != ds.Ino || m.LockDevice != uint64(ls.Dev) || m.LockInode != ls.Ino {
		return nil, denied("marker identity")
	}
	hash, err := c.executableHash()
	if err != nil || m.BinarySHA256 != hash {
		return nil, denied("executable identity")
	}
	if !sameLock(c, authority, ls) {
		return nil, denied("lock identity")
	}
	keep = true
	return &Lease{fd: lock}, nil
}

func openPath(path string, directory bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "admission"), nil
}

func openAt(dir *os.File, name string, directory bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(dir.Fd()), name, flags, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "admission"), nil
}

func checkedStat(c runtimeChecks, f *os.File, kind uint32, owner uint32, mode uint32) (unix.Stat_t, error) {
	s, err := c.stat(int(f.Fd()))
	if err != nil || s.Mode&unix.S_IFMT != kind || s.Mode&07777 != mode || s.Uid != owner || s.Gid != privateID {
		return unix.Stat_t{}, denied("file metadata")
	}
	return s, nil
}

func sameLock(c runtimeChecks, authority *os.File, want unix.Stat_t) bool {
	f, err := openAt(authority, "runtime.lock", false)
	if err != nil {
		return false
	}
	defer f.Close()
	s, err := checkedStat(c, f, unix.S_IFREG, 0, 0640)
	return err == nil && s.Dev == want.Dev && s.Ino == want.Ino
}

func descriptorStat(fd int) (unix.Stat_t, error) {
	var s unix.Stat_t
	err := unix.Fstat(fd, &s)
	return s, err
}
func filesystemReadOnly(fd int) (bool, error) {
	var s unix.Statfs_t
	err := unix.Fstatfs(fd, &s)
	return s.Flags&unix.ST_RDONLY != 0, err
}
