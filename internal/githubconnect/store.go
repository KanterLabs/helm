package githubconnect

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"
	"time"

	"github.com/KanterLabs/helm/internal/githubprovider"
)

const (
	stateFileName   = "connection.json"
	maxStateFile    = 16 << 10
	recordVersion   = 1
	tempPattern     = ".connection-*.tmp"
	maxRepositories = 50
)

// geteuid is the owner the state directory and file must have.
var geteuid = os.Geteuid

// record is the whole of contract C3. An ended sign-in keeps the account
// and drops both tokens, so reconnect_required survives a restart.
type record struct {
	Version               int       `json:"version"`
	ID                    string    `json:"id"`
	AccessToken           string    `json:"access_token"`
	RefreshToken          string    `json:"refresh_token"`
	AccessExpiresAt       time.Time `json:"access_expires_at"`  // zero: the token does not expire
	RefreshExpiresAt      time.Time `json:"refresh_expires_at"` // zero: unknown or none
	Login                 string    `json:"login"`
	UserID                int64     `json:"user_id"`
	ConnectedBy           string    `json:"connected_by"`
	ConnectedAt           time.Time `json:"connected_at"`
	Repositories          []string  `json:"repositories"`
	RepositoriesTruncated bool      `json:"repositories_truncated"`
	RepositoriesCheckedAt time.Time `json:"repositories_checked_at"`
}

var (
	idPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	loginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
)

// validToken: 1 to 4096 printable ASCII characters, so no whitespace or
// control character can reach an Authorization header.
func validToken(token string) bool {
	if token == "" || len(token) > 4096 {
		return false
	}
	for i := 0; i < len(token); i++ {
		if token[i] <= ' ' || token[i] >= 0x7f {
			return false
		}
	}
	return true
}

func validActor(actor string) bool {
	if actor == "" || len(actor) > 256 {
		return false
	}
	for i := 0; i < len(actor); i++ {
		if actor[i] < ' ' || actor[i] == 0x7f {
			return false
		}
	}
	return true
}

// validRepositories: normalized, sorted, unique, at most 50.
func validRepositories(list []string) bool {
	if len(list) > maxRepositories || !slices.IsSorted(list) {
		return false
	}
	for i, repository := range list {
		if normalized, err := githubprovider.NormalizeRepository(repository); err != nil || normalized != repository || (i > 0 && list[i-1] == repository) {
			return false
		}
	}
	return true
}

func (r *record) valid() bool {
	if r.Version != recordVersion || !idPattern.MatchString(r.ID) || !loginPattern.MatchString(r.Login) || r.UserID < 1 {
		return false
	}
	if !validActor(r.ConnectedBy) || r.ConnectedAt.IsZero() || !validRepositories(r.Repositories) {
		return false
	}
	if r.AccessToken == "" {
		return r.RefreshToken == ""
	}
	return validToken(r.AccessToken) && (r.RefreshToken == "" || validToken(r.RefreshToken))
}

// fileStore keeps the record in one 0600 file in a 0700 directory.
type fileStore struct {
	dir, path string
	// refused hears why the directory was refused.
	refused func(reason string)
	// beforeRename is a test seam: a failure after the temp file is written.
	beforeRename func() error
}

func newFileStore(dir string) *fileStore {
	return &fileStore{dir: dir, path: filepath.Join(dir, stateFileName)}
}

// checkDir follows a symlinked directory (operators link data directories)
// but never trusts, and never re-permissions, one that another user owns or
// that group or others can reach: it may be shared on purpose.
func (s *fileStore) checkDir() (exists bool, ok bool) {
	info, err := os.Stat(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, true
	}
	if err != nil || !info.IsDir() {
		s.refuse("state directory path is not a readable directory")
		return true, false
	}
	stat, isUnix := info.Sys().(*syscall.Stat_t)
	if !isUnix || int(stat.Uid) != geteuid() || info.Mode().Perm()&0o077 != 0 {
		s.refuse("state directory must belong to the Helm user and be closed to group and others (mode 0700)")
		return true, false
	}
	return true, true
}

func (s *fileStore) refuse(reason string) {
	if s.refused != nil {
		s.refused(reason)
	}
}

// load returns the record, nothing for a missing file, or broken for any
// file it will not trust. A broken file is left in place.
func (s *fileStore) load() (rec *record, broken bool) {
	exists, ok := s.checkDir()
	if !exists {
		return nil, false
	}
	info, err := os.Lstat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if !ok || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maxStateFile {
		return nil, true
	}
	if stat, isUnix := info.Sys().(*syscall.Stat_t); !isUnix || int(stat.Uid) != geteuid() {
		return nil, true
	}
	file, err := os.OpenFile(s.path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, true
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, true
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxStateFile+1))
	if err != nil || len(raw) > maxStateFile {
		return nil, true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	rec = &record{}
	if err := decoder.Decode(rec); err != nil || decoder.More() || !rec.valid() {
		return nil, true
	}
	return rec, false
}

// save writes a temp file, fsyncs it, renames it over the old file and
// fsyncs the directory. A failure leaves the old file untouched.
func (s *fileStore) save(rec *record) error {
	storageFailed := fail(CodeStorageFailed)
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil || len(raw) > maxStateFile {
		return storageFailed
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		s.refuse("state directory could not be created")
		return storageFailed
	}
	if _, ok := s.checkDir(); !ok {
		return storageFailed
	}
	tmp, err := os.CreateTemp(s.dir, tempPattern)
	if err != nil {
		return storageFailed
	}
	name, done := tmp.Name(), false
	defer func() {
		if !done {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return storageFailed
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return storageFailed
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return storageFailed
	}
	if err := tmp.Close(); err != nil {
		return storageFailed
	}
	if s.beforeRename != nil {
		if err := s.beforeRename(); err != nil {
			return storageFailed
		}
	}
	if err := os.Rename(name, s.path); err != nil {
		return storageFailed
	}
	done = true
	s.syncDir()
	return nil
}

// remove deletes the credential (or an untrusted file in its place).
func (s *fileStore) remove() error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fail(CodeStorageFailed)
	}
	s.syncDir()
	return nil
}

func (s *fileStore) syncDir() {
	if dir, err := os.Open(s.dir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
}

// cleanTemps removes temp files a crash left behind.
func (s *fileStore) cleanTemps() {
	if _, ok := s.checkDir(); !ok {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(s.dir, tempPattern))
	for _, name := range matches {
		if info, err := os.Lstat(name); err == nil && info.Mode().IsRegular() {
			_ = os.Remove(name)
		}
	}
}
