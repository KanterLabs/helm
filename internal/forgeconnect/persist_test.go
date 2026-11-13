package forgeconnect

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLastCheckSurvivesRestart(t *testing.T) {
	e := newEnv(t)
	e.forge.set(func(f *fakeForge) { f.repos = 30 })
	e.connect()
	if got := e.open().Status(); got.RepositoryCount != 30 || got.Check != (CheckResult{State: CheckOK}) {
		t.Fatalf("after sign-in and restart = %+v", got)
	}

	e.forge.set(func(f *fakeForge) { f.repos = 42 })
	e.m.Check(e.ctx())
	if got := e.open().Status(); got.RepositoryCount != 42 || got.Check != (CheckResult{State: CheckOK}) {
		t.Fatalf("after check and restart = %+v", got)
	}

	e.forge.set(func(f *fakeForge) { f.userStatus = http.StatusInternalServerError })
	e.m.Check(e.ctx())
	want := CheckResult{State: CheckUnreachable, Code: CodeServerUnreachable}
	if got := e.open().Status(); got.RepositoryCount != 42 || got.Check != want || got.State != StateConnected {
		t.Fatalf("after failed check and restart = %+v", got)
	}

	// A file written before the fields existed still loads.
	path := filepath.Join(e.dir, stateFileName)
	rewrite := func(change func(map[string]any)) {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		change(fields)
		raw, _ = json.Marshal(fields)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rewrite(func(fields map[string]any) { delete(fields, "repository_count"); delete(fields, "check") })
	if got := e.open().Status(); got.State != StateConnected || got.RepositoryCount != 0 || got.Check != (CheckResult{}) {
		t.Fatalf("older version 1 file = %+v", got)
	}

	for name, bad := range map[string]func(map[string]any){
		"negative count": func(f map[string]any) { f["repository_count"] = -1 },
		"huge count":     func(f map[string]any) { f["repository_count"] = 2_000_000_000 },
		"unknown state":  func(f map[string]any) { f["check"] = map[string]any{"state": upstreamSentinel, "code": ""} },
		"unknown code":   func(f map[string]any) { f["check"] = map[string]any{"state": "unreachable", "code": upstreamSentinel} },
	} {
		rewrite(func(fields map[string]any) {
			fields["repository_count"] = 1
			fields["check"] = map[string]any{"state": "ok", "code": ""}
		})
		rewrite(bad)
		got := e.open().Status()
		if got.State != StateReconnectRequired {
			t.Fatalf("%s: state = %q", name, got.State)
		}
		if raw, _ := json.Marshal(got); strings.Contains(string(raw), upstreamSentinel) {
			t.Fatalf("%s: stored value echoed: %s", name, raw)
		}
	}
}
