package forgeconnect

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

const (
	stateFileName = "gitea.json"
	maxStateFile  = 32 << 10
	recordVersion = 1
	tempPattern   = ".gitea-*.tmp"
)

// Access values: what the limit check found.
const (
	AccessScoped  = "scoped"
	AccessFull    = "full"
	AccessUnknown = "unknown"
)

// Server is a forge Helm has found.
type Server struct {
	BaseURL      string `json:"base_url"`
	Product      string `json:"product"`
	Version      string `json:"version"`
	InsecureHTTP bool   `json:"insecure_http"`
}

// Account is the forge account Helm signed in as.
type Account struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

// record is the whole of contract C2.
type record struct {
	Version         int       `json:"version"`
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	Server          Server    `json:"server"`
	ClientID        string    `json:"client_id"`
	AccessToken     string    `json:"access_token"`
	RefreshToken    string    `json:"refresh_token"`
	AccessExpiresAt time.Time `json:"access_expires_at"`
	RefreshedAt     time.Time `json:"refreshed_at"`
	Account         Account   `json:"account"`
	Access          string    `json:"access"`
	ConnectedBy     string    `json:"connected_by"`
	ConnectedAt     time.Time `json:"connected_at"`
	CheckedAt       time.Time `json:"checked_at"`
}

var versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_~-]{0,63}$`)

func validVersion(v string) bool { return versionPattern.MatchString(v) }

// validToken: non-empty, at most 8 KB, no whitespace or control characters.
func validToken(token string) bool {
	if token == "" || len(token) > 8<<10 {
		return false
	}
	for i := 0; i < len(token); i++ {
		if token[i] <= ' ' || token[i] == 0x7f {
			return false
		}
	}
	return true
}

func (r *record) valid() bool {
	if r.Version != recordVersion || r.Kind != gitea.Kind || r.ID == "" || len(r.ID) > 64 || len(r.ConnectedBy) > 256 {
		return false
	}
	if base, err := normalizeBaseURL(r.Server.BaseURL, r.Server.InsecureHTTP); err != nil || base != r.Server.BaseURL {
		return false
	}
	if r.Server.Product != gitea.Product && r.Server.Product != gitea.ForkProduct || r.Server.Version != "" && !validVersion(r.Server.Version) {
		return false
	}
	if !validClientID(r.ClientID) || r.AccessToken != "" && !validToken(r.AccessToken) || r.RefreshToken != "" && !validToken(r.RefreshToken) {
		return false
	}
	if r.Access != AccessScoped && r.Access != AccessFull && r.Access != AccessUnknown {
		return false
	}
	return r.Account.Login != "" && len(r.Account.Login) <= 255
}

// fileStore keeps the record in one 0600 file in a 0700 directory.
type fileStore struct {
	dir, path string
	uid       func() int
	// beforeRename is a test seam for a write failure after the temp file
	// is written.
	beforeRename func() error
}

func newFileStore(dir string) *fileStore {
	return &fileStore{dir: dir, path: filepath.Join(dir, stateFileName), uid: os.Geteuid}
}

// load returns the record, nothing for a missing file, or broken for any
// file it will not trust. A broken file is left in place.
func (s *fileStore) load() (rec *record, broken bool) {
	info, err := os.Lstat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maxStateFile {
		return nil, true
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != s.uid() {
		return nil, true
	}
	file, err := os.OpenFile(s.path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, true
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Mode().Perm()&0o077 != 0 {
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
		return storageFailed
	}
	info, err := os.Lstat(s.dir)
	if err != nil || !info.IsDir() {
		return storageFailed
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(s.dir, 0o700); err != nil {
			return storageFailed
		}
	}
	tmp, err := os.CreateTemp(s.dir, tempPattern)
	if err != nil {
		return storageFailed
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
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
	ok = true
	s.syncDir()
	return nil
}

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
	matches, _ := filepath.Glob(filepath.Join(s.dir, tempPattern))
	for _, name := range matches {
		if info, err := os.Lstat(name); err == nil && info.Mode().IsRegular() {
			_ = os.Remove(name)
		}
	}
}
