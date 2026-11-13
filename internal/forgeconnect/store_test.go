package forgeconnect

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleRecord() *record {
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	return &record{
		Version: 1, ID: "conn-1", Kind: "gitea",
		Server:   Server{BaseURL: "https://gitea.example", Product: "gitea", Version: "28.3.0"},
		ClientID: testClientID, AccessToken: accessSentinel + "-store", RefreshToken: refreshSentinel + "-store",
		AccessExpiresAt: at.Add(time.Hour), RefreshedAt: at, Account: Account{Login: "helm-bot", ID: 7},
		Access: AccessScoped, ConnectedBy: testActor, ConnectedAt: at, CheckedAt: at,
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "forge")
	s := newFileStore(dir)
	if rec, broken := s.load(); rec != nil || broken {
		t.Fatal("missing file must load as nothing")
	}
	want := sampleRecord()
	if err := s.save(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "gitea.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v %v", info.Mode(), err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v %v", info.Mode(), err)
	}
	got, broken := s.load()
	if broken || got == nil || got.AccessToken != want.AccessToken || got.Account != want.Account || !got.AccessExpiresAt.Equal(want.AccessExpiresAt) {
		t.Fatalf("round trip = %+v broken=%v", got, broken)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "gitea.json"))
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	for _, key := range []string{"version", "id", "kind", "server", "client_id", "access_token", "refresh_token", "access_expires_at", "refreshed_at", "account", "access", "connected_by", "connected_at", "checked_at"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("file lacks %q", key)
		}
	}
	if len(fields) != 14 {
		t.Errorf("file has %d fields, want 14", len(fields))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	if err := s.remove(); err != nil {
		t.Fatal(err)
	}
	if err := s.remove(); err != nil {
		t.Fatal("removing a missing file must succeed")
	}
}

func TestStoreLoadRejects(t *testing.T) {
	valid, _ := json.Marshal(sampleRecord())
	mutate := func(change func(map[string]any)) []byte {
		var fields map[string]any
		_ = json.Unmarshal(valid, &fields)
		change(fields)
		raw, _ := json.Marshal(fields)
		return raw
	}
	contents := map[string][]byte{
		"truncated":       valid[:len(valid)/2],
		"empty":           {},
		"not an object":   []byte(`[1,2]`),
		"unknown version": mutate(func(f map[string]any) { f["version"] = 2 }),
		"unknown kind":    mutate(func(f map[string]any) { f["kind"] = "gitlab" }),
		"bad base URL":    mutate(func(f map[string]any) { f["server"].(map[string]any)["base_url"] = "https://u:p@x.example" }),
		"http not marked": mutate(func(f map[string]any) { f["server"].(map[string]any)["base_url"] = "http://x.example" }),
		"bad client ID":   mutate(func(f map[string]any) { f["client_id"] = "a b" }),
		"bad token":       mutate(func(f map[string]any) { f["access_token"] = "a b" }),
		"bad access":      mutate(func(f map[string]any) { f["access"] = "admin" }),
		"bad product":     mutate(func(f map[string]any) { f["server"].(map[string]any)["product"] = "gitlab" }),
		"no id":           mutate(func(f map[string]any) { f["id"] = "" }),
		"oversized":       append(append([]byte{}, valid[:len(valid)-1]...), []byte(`,"pad":"`+strings.Repeat("x", 33<<10)+`"}`)...),
	}
	for name, raw := range contents {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "gitea.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if rec, broken := newFileStore(dir).load(); rec != nil || !broken {
				t.Fatalf("loaded %+v broken=%v", rec, broken)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("a rejected file must be left in place")
			}
		})
	}
	t.Run("loose permissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "gitea.json")
		_ = os.WriteFile(path, valid, 0o600)
		_ = os.Chmod(path, 0o640)
		if rec, broken := newFileStore(dir).load(); rec != nil || !broken {
			t.Fatal("group-readable file loaded")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "real.json")
		_ = os.WriteFile(target, valid, 0o600)
		_ = os.Symlink(target, filepath.Join(dir, "gitea.json"))
		if rec, broken := newFileStore(dir).load(); rec != nil || !broken {
			t.Fatal("symlink loaded")
		}
	})
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.Mkdir(filepath.Join(dir, "gitea.json"), 0o700)
		if rec, broken := newFileStore(dir).load(); rec != nil || !broken {
			t.Fatal("directory loaded")
		}
	})
	t.Run("other owner", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "gitea.json"), valid, 0o600)
		s := newFileStore(dir)
		s.uid = func() int { return os.Geteuid() + 1 }
		if rec, broken := s.load(); rec != nil || !broken {
			t.Fatal("file owned by another user loaded")
		}
	})
}

func TestStorePartialWriteLeavesOldFile(t *testing.T) {
	dir := t.TempDir()
	s := newFileStore(dir)
	old := sampleRecord()
	if err := s.save(old); err != nil {
		t.Fatal(err)
	}
	// A crash mid-write leaves a temp file beside the real one.
	stray := filepath.Join(dir, ".gitea-123.tmp")
	_ = os.WriteFile(stray, []byte(`{"version":1,"trunc`), 0o600)
	got, broken := s.load()
	if broken || got == nil || got.AccessToken != old.AccessToken {
		t.Fatal("stray temp file disturbed the real file")
	}
	s.cleanTemps()
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatal("stray temp file not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "gitea.json")); err != nil {
		t.Fatal("cleanup removed the real file")
	}
}

func TestStoreWriteFailure(t *testing.T) {
	dir := t.TempDir()
	s := newFileStore(dir)
	old := sampleRecord()
	_ = s.save(old)
	s.beforeRename = func() error { return errors.New("disk full") }
	next := sampleRecord()
	next.AccessToken = accessSentinel + "-next"
	if err := s.save(next); Code(err) != CodeStorageFailed {
		t.Fatalf("save = %v", err)
	}
	got, _ := s.load()
	if got.AccessToken != old.AccessToken {
		t.Fatal("failed write replaced the file")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp file left after failure: %v", entries)
	}
	// Sign-in that cannot be saved is not kept in memory either.
	e := newEnv(t)
	e.m.store.beforeRename = func() error { return errors.New("read-only") }
	code, state := e.forge.approve(e.begin(testActor))
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")), CodeStorageFailed)
	if e.m.Status().State != StateDisconnected {
		t.Fatal("unsaved connection kept in memory")
	}
	// A state directory that is a file cannot be written.
	file := filepath.Join(t.TempDir(), "plain")
	_ = os.WriteFile(file, nil, 0o600)
	if err := newFileStore(file).save(sampleRecord()); Code(err) != CodeStorageFailed {
		t.Fatalf("save into a file = %v", err)
	}
}
