package githubconnect

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/githubprovider"
)

func (e *env) file() string { return filepath.Join(e.dir, stateFileName) }

func (e *env) read() []byte {
	e.t.Helper()
	raw, err := os.ReadFile(e.file())
	if err != nil {
		e.t.Fatal(err)
	}
	return raw
}

func TestSavedFileIsPrivate(t *testing.T) {
	e := newEnv(t)
	e.connect()
	file, err := os.Stat(e.file())
	if err != nil || file.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, %v", file.Mode(), err)
	}
	dir, err := os.Stat(e.dir)
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v, %v", dir.Mode(), err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(e.read(), &fields); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	want := []string{"access_expires_at", "access_token", "connected_at", "connected_by", "id", "login", "refresh_expires_at",
		"refresh_token", "repositories", "repositories_checked_at", "repositories_truncated", "user_id", "version"}
	if !slices.Equal(keys, want) || fields["version"] != float64(1) || fields["connected_by"] != testActor {
		t.Fatalf("file fields %v", fields)
	}
	entries, _ := os.ReadDir(e.dir)
	if len(entries) != 1 {
		t.Fatalf("state directory holds %d entries", len(entries))
	}
}

func TestUntrustedFilesAreReconnectRequired(t *testing.T) {
	seed := newEnv(t)
	seed.connect()
	good := seed.read()
	mutate := func(f func(map[string]any)) []byte {
		fields := map[string]any{}
		_ = json.Unmarshal(good, &fields)
		f(fields)
		raw, _ := json.Marshal(fields)
		return raw
	}
	cases := map[string]func(t *testing.T, path string){
		"loose mode": func(t *testing.T, path string) { write(t, path, good, 0o640) },
		"symlink": func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "real.json")
			write(t, target, good, 0o600)
			must(t, os.Symlink(target, path))
		},
		"directory": func(t *testing.T, path string) { must(t, os.Mkdir(path, 0o700)) },
		"oversized": func(t *testing.T, path string) {
			write(t, path, append(bytes.Clone(good), bytes.Repeat([]byte(" "), 17<<10)...), 0o600)
		},
		"truncated": func(t *testing.T, path string) { write(t, path, good[:len(good)/2], 0o600) },
		"version": func(t *testing.T, path string) {
			write(t, path, mutate(func(m map[string]any) { m["version"] = 2 }), 0o600)
		},
		"unknown field": func(t *testing.T, path string) {
			write(t, path, mutate(func(m map[string]any) { m["extra"] = true }), 0o600)
		},
		"spaced token": func(t *testing.T, path string) {
			write(t, path, mutate(func(m map[string]any) { m["access_token"] = "ghu_" + accessSentinel + " x" }), 0o600)
		},
		"bad repository": func(t *testing.T, path string) {
			write(t, path, mutate(func(m map[string]any) { m["repositories"] = []string{"../etc"} }), 0o600)
		},
		"wrong owner": func(t *testing.T, path string) {
			write(t, path, good, 0o600)
			real := geteuid
			geteuid = func() int { return real() + 1 }
			t.Cleanup(func() { geteuid = real })
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			must(t, os.MkdirAll(e.dir, 0o700))
			setup(t, e.file())
			e.restart()
			if s := e.m.Status(testActor); s.State != StateReconnectRequired {
				t.Fatalf("state %q", s.State)
			}
			_, _, err := e.m.Current()
			if githubprovider.ErrorCode(e.err(err)) != "reconnect_required" {
				t.Fatalf("Current: %v", err)
			}
			if _, err := os.Lstat(e.file()); err != nil {
				t.Fatalf("untrusted file removed: %v", err)
			}
			if len(e.m.Repositories()) != 0 {
				t.Fatal("untrusted file produced an allowlist")
			}
		})
	}
}

func TestLooseDirectoryIsRefusedAndExplained(t *testing.T) {
	e := newEnv(t)
	must(t, os.Mkdir(e.dir, 0o750))
	must(t, os.Chmod(e.dir, 0o750))
	e.start()
	e.gh.set(func(g *fakeGitHub) { g.pollScript = []string{"success"} })
	e.begin(testActor)
	e.step()
	s := e.m.Status(testActor)
	if s.State != StateDisconnected || s.Attempt == nil || s.Attempt.State != AttemptFailed {
		t.Fatalf("status %+v / %+v", s, s.Attempt)
	}
	info, _ := os.Stat(e.dir)
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("directory was re-permissioned to %v", info.Mode().Perm())
	}
	if _, err := os.Stat(e.file()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential written into a loose directory: %v", err)
	}
	refusals := 0
	for _, line := range e.logLines() {
		if strings.Contains(line, "github state directory refused") {
			refusals++
			if !strings.Contains(line, "0700") {
				t.Fatalf("refusal does not say why: %s", line)
			}
		}
	}
	logs := strings.Join(e.logLines(), "\n")
	if refusals != 1 || !strings.Contains(logs, `"code":"storage_failed"`) {
		t.Fatalf("logs:\n%s", logs)
	}
}

func TestSymlinkedDirectoryIsFollowed(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real")
	must(t, os.Mkdir(real, 0o700))
	link := filepath.Join(t.TempDir(), "link")
	must(t, os.Symlink(real, link))
	e := newEnv(t, func(o *Options) { o.StateDir = link })
	e.dir = link
	e.connect()
	if _, err := os.Stat(filepath.Join(real, stateFileName)); err != nil {
		t.Fatalf("credential not in the linked directory: %v", err)
	}
	e.restart()
	if s := e.m.Status(testActor); s.State != StateConnected {
		t.Fatalf("after restart: %q", s.State)
	}
}

func TestWriteFailureKeepsTheOldFile(t *testing.T) {
	e := newEnv(t)
	e.connect()
	before := e.read()
	e.m.store.beforeRename = func() error { return errors.New("disk full") }
	e.gh.set(func(g *fakeGitHub) { g.installations[0].repos = append(g.installations[0].repos, "KanterLabs/new") })
	wantCode(t, e.err(e.m.Sync(e.ctx())), CodeStorageFailed)
	if !bytes.Equal(before, e.read()) {
		t.Fatal("a failed write changed the file")
	}
	entries, _ := os.ReadDir(e.dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left: %d entries", len(entries))
	}
}

func TestCrashLeftoversAreRemoved(t *testing.T) {
	e := newEnv(t)
	e.connect()
	leftover := filepath.Join(e.dir, ".connection-12345.tmp")
	write(t, leftover, []byte(`{"version":1,"access_token":"`+accessSentinel), 0o600)
	e.restart()
	if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover temp file kept: %v", err)
	}
	if s := e.m.Status(testActor); s.State != StateConnected {
		t.Fatalf("a crash leftover broke the connection: %q", s.State)
	}
}

func TestDisconnectDeletesTheCredential(t *testing.T) {
	e := newEnv(t)
	e.connect()
	_, first, _ := e.m.Current()
	must(t, e.err(e.m.Disconnect(testActor)))
	if _, err := os.Stat(e.file()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential kept: %v", err)
	}
	if s := e.m.Status(testActor); s.State != StateDisconnected || s.Login != "" {
		t.Fatalf("status %+v", s)
	}
	_, _, err := e.m.Current()
	if githubprovider.ErrorCode(e.err(err)) != "not_configured" {
		t.Fatalf("Current after disconnect: %v", err)
	}
	must(t, e.err(e.m.Disconnect(testActor))) // idempotent
	e.gh.set(func(g *fakeGitHub) { g.pollScript = []string{"success"} })
	e.begin(testActor)
	e.step()
	_, second, err := e.m.Current()
	if e.err(err) != nil || second == first {
		t.Fatalf("reconnect kept identity %q (%v)", second, err)
	}
}

func write(t *testing.T, path string, raw []byte, mode os.FileMode) {
	t.Helper()
	must(t, os.WriteFile(path, raw, mode))
	must(t, os.Chmod(path, mode))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
