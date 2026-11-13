package forgeconnect

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestLimitCheck(t *testing.T) {
	atSignIn := map[int]string{http.StatusForbidden: AccessScoped, http.StatusOK: AccessFull, http.StatusInternalServerError: AccessUnknown, http.StatusNotFound: AccessUnknown, http.StatusUnauthorized: AccessUnknown}
	for status, want := range atSignIn {
		e := newEnv(t)
		e.forge.set(func(f *fakeForge) { f.orgsStatus = status })
		e.connect()
		if got := e.m.Status().Access; got != want {
			t.Errorf("sign-in limit check %d = %q, want %q", status, got, want)
		}
	}
	e := newEnv(t)
	e.connect()
	for _, c := range []struct {
		status int
		access string
	}{{http.StatusOK, AccessFull}, {http.StatusForbidden, AccessScoped}, {http.StatusInternalServerError, AccessUnknown}} {
		e.forge.set(func(f *fakeForge) { f.orgsStatus = c.status })
		status := e.m.Check(e.ctx())
		if status.Access != c.access || status.Check.State != CheckOK || e.stored().Access != c.access {
			t.Fatalf("check limit %d = %+v", c.status, status)
		}
	}
	// 401 on the limit check: one refresh and retry, then the refreshed
	// token is rejected again within a minute.
	e.forge.set(func(f *fakeForge) { f.orgsStatus = http.StatusUnauthorized })
	status := e.m.Check(e.ctx())
	if status.Check.State != CheckUnauthorized || status.Check.Code != CodeReconnectRequired || status.State != StateReconnectRequired || e.forge.refreshes() != 1 {
		t.Fatalf("check limit 401 = %+v (refreshes %d)", status, e.forge.refreshes())
	}
}

func TestCheckOutcomes(t *testing.T) {
	e := newEnv(t)
	e.forge.set(func(f *fakeForge) { f.repos = 30 })
	e.connect()
	e.clock.Advance(10 * time.Minute)
	status := e.m.Check(e.ctx())
	if status.Check != (CheckResult{State: CheckOK}) || status.RepositoryCount != 30 || status.CheckedAt != e.clock.Now().Format(time.RFC3339) {
		t.Fatalf("ok check = %+v", status)
	}
	if !e.stored().CheckedAt.Equal(e.clock.Now()) {
		t.Fatal("checked_at not persisted")
	}
	for _, header := range []string{"abc", "", "-4", "99999999999999999999"} {
		header := header
		e.forge.set(func(f *fakeForge) { f.totalHeader = &header; f.repos = 3 })
		if got := e.m.Check(e.ctx()); got.RepositoryCount != 30 || got.Check.State != CheckOK {
			t.Fatalf("X-Total-Count %q = %+v", header, got)
		}
	}
	e.forge.set(func(f *fakeForge) { f.userStatus = http.StatusInternalServerError })
	if got := e.m.Check(e.ctx()); got.Check != (CheckResult{State: CheckUnreachable, Code: CodeServerUnreachable}) || got.State != StateConnected {
		t.Fatalf("identity 500 = %+v", got)
	}
	e.forge.set(func(f *fakeForge) { f.userStatus = 0 })
	e.forge.srv.Close()
	e.clock.Advance(time.Minute)
	got := e.m.Check(e.ctx())
	if got.Check != (CheckResult{State: CheckUnreachable, Code: CodeServerUnreachable}) || got.State != StateConnected || got.CheckedAt != e.clock.Now().Format(time.RFC3339) {
		t.Fatalf("server down = %+v", got)
	}

	revoked := newEnv(t)
	revoked.connect()
	revoked.forge.set(func(f *fakeForge) { f.revoked = true })
	got = revoked.m.Check(revoked.ctx())
	if got.Check != (CheckResult{State: CheckUnauthorized, Code: CodeReconnectRequired}) || got.State != StateReconnectRequired || got.Account == nil {
		t.Fatalf("revoked = %+v", got)
	}

	idle := newEnv(t)
	if got := idle.m.Check(idle.ctx()); got.State != StateDisconnected || idle.forge.totalCalls() != 0 {
		t.Fatal("check while disconnected must not contact anything")
	}
}

func TestRepositoriesPagination(t *testing.T) {
	cases := []struct {
		repos     int
		header    *string
		want      int
		truncated bool
		pages     int
	}{
		{0, nil, 0, false, 1},
		{12, nil, 12, false, 1},
		{137, nil, 137, false, 3},
		{500, nil, 500, false, 10},
		{501, nil, 500, true, 10},
		{520, new(string), 500, true, 10},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.repos), func(t *testing.T) {
			e := newEnv(t)
			e.connect()
			e.forge.set(func(f *fakeForge) { f.repos = c.repos; f.totalHeader = c.header })
			before := e.forge.count("/api/v1/user/repos")
			list, truncated, err := e.m.Repositories(e.ctx())
			if e.err(err) != nil || len(list) != c.want || truncated != c.truncated || e.forge.count("/api/v1/user/repos")-before != c.pages {
				t.Fatalf("got %d truncated=%v pages=%d err=%v", len(list), truncated, e.forge.count("/api/v1/user/repos")-before, err)
			}
			if c.want > 0 && list[0] != (Repository{FullName: "team/repo-000", Private: true, Archived: true}) {
				t.Fatalf("first = %+v", list[0])
			}
		})
	}
	for name, change := range map[string]func(*fakeForge){
		"malformed": func(f *fakeForge) { f.repoBody = `{"not":"a list"` },
		"500":       func(f *fakeForge) { f.reposStatus = http.StatusInternalServerError },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.connect()
			e.forge.set(change)
			_, _, err := e.m.Repositories(e.ctx())
			wantCode(t, e.err(err), CodeServerUnreachable)
		})
	}
	encoded, _ := json.Marshal(Repository{FullName: "a/b", Private: true})
	if string(encoded) != `{"full_name":"a/b","private":true,"archived":false}` {
		t.Fatalf("repository JSON = %s", encoded)
	}
}

func TestRepositoriesOversizedPage(t *testing.T) {
	e := newEnv(t)
	e.connect()
	padding := strings.Repeat("x", 2000)
	var items []string
	for i := 0; i < 50; i++ {
		items = append(items, fmt.Sprintf(`{"full_name":"team/r%d","private":false,"archived":false,"description":"%s"}`, i, padding))
	}
	total := "50"
	e.forge.set(func(f *fakeForge) { f.repoBody = "[" + strings.Join(items, ",") + "]"; f.totalHeader = &total })
	if list, _, err := e.m.Repositories(e.ctx()); e.err(err) != nil || len(list) != 50 {
		t.Fatalf("a 100 KB page must be accepted: %d %v", len(list), err)
	}
	e.forge.set(func(f *fakeForge) { f.repoBody = `[{"full_name":"` + strings.Repeat("y", 1100<<10) + `"}]` })
	_, _, err := e.m.Repositories(e.ctx())
	wantCode(t, e.err(err), CodeServerUnreachable)
}

func TestStatusNoNetwork(t *testing.T) {
	e := newEnv(t)
	if e.forge.totalCalls() != 0 {
		t.Fatal("New contacted the network")
	}
	e.connect()
	calls := e.forge.totalCalls()
	for i := 0; i < 3; i++ {
		_ = e.m.Status()
	}
	e.clock.Advance(48 * time.Hour)
	_ = e.m.Status()
	restarted := e.open()
	_ = restarted.Status()
	_, _, _ = restarted.Current()
	if e.forge.totalCalls() != calls {
		t.Fatal("Status, Current or New did network I/O")
	}
}

func keysOf(t *testing.T, value any) []string {
	t.Helper()
	raw, _ := json.Marshal(value)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestStatusJSONShape(t *testing.T) {
	e := newEnv(t)
	base := []string{"access", "available", "check", "checked_at", "connected_at", "redirect_uri", "repository_count", "state", "unavailable_reason"}
	if got := keysOf(t, e.m.Status()); !reflect.DeepEqual(got, base) {
		t.Fatalf("disconnected keys = %v", got)
	}
	e.connect()
	status := e.m.Status()
	full := append(append([]string{}, base...), "account", "client_id", "register_url", "revoke_url", "server")
	sort.Strings(full)
	if got := keysOf(t, status); !reflect.DeepEqual(got, full) {
		t.Fatalf("connected keys = %v", got)
	}
	if got := keysOf(t, status.Server); !reflect.DeepEqual(got, []string{"base_url", "insecure_http", "product", "version"}) {
		t.Fatalf("server keys = %v", got)
	}
	if got := keysOf(t, status.Account); !reflect.DeepEqual(got, []string{"id", "login"}) {
		t.Fatalf("account keys = %v", got)
	}
	if got := keysOf(t, status.Check); !reflect.DeepEqual(got, []string{"code", "state"}) {
		t.Fatalf("check keys = %v", got)
	}
	if status.RegisterURL != e.forge.base()+"/user/settings/applications" || status.RevokeURL != status.RegisterURL ||
		status.RedirectURI != testOrigin+"/api/v1/integrations/gitea/oauth/callback" || !status.Server.InsecureHTTP ||
		status.ConnectedAt != e.clock.Now().Format(time.RFC3339) || status.Account.ID != 7 {
		t.Fatalf("status = %+v", status)
	}
}

func TestStatusDisconnected(t *testing.T) {
	e := newEnv(t)
	status := e.m.Status()
	if !status.Available || status.State != StateDisconnected || status.Server != nil {
		t.Fatalf("status = %+v", status)
	}
	_ = os.MkdirAll(e.dir, 0o700)
	_ = os.WriteFile(filepath.Join(e.dir, "gitea.json"), []byte("{trunc"), 0o600)
	broken := e.open()
	if got := broken.Status(); got.State != StateReconnectRequired || got.Server != nil || got.ClientID != "" {
		t.Fatalf("broken file status = %+v", got)
	}
	_, err := broken.Begin(e.ctx(), testActor, BeginInput{ClientID: testClientID})
	wantCode(t, e.err(err), CodeInvalidServerURL)
	e.m = broken
	e.connect()
	if e.stored() == nil {
		t.Fatal("sign-in did not replace the broken file")
	}
}

func TestDisconnect(t *testing.T) {
	e := newEnv(t)
	e.connect()
	_, first, _ := e.m.Current()
	if err := e.m.Disconnect(testActor); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "gitea.json")); !os.IsNotExist(err) {
		t.Fatal("file left after disconnect")
	}
	if status := e.m.Status(); status.State != StateDisconnected || status.Server != nil || status.Account != nil || status.RepositoryCount != 0 {
		t.Fatalf("status after disconnect = %+v", status)
	}
	_, _, err := e.m.Current()
	wantCode(t, e.err(err), CodeUnavailable)
	_, _, err = e.m.Fresh(e.ctx())
	wantCode(t, e.err(err), CodeUnavailable)
	_, _, err = e.m.Repositories(e.ctx())
	wantCode(t, e.err(err), CodeUnavailable)
	if e.m.BaseURL() != "" {
		t.Fatal("BaseURL after disconnect")
	}
	if err := e.m.Disconnect(testActor); err != nil {
		t.Fatal("disconnecting twice must succeed")
	}
	e.connect()
	if _, second, _ := e.m.Current(); second == first {
		t.Fatal("identity must change on connect")
	}
	// A path that cannot be removed.
	_ = e.m.Disconnect(testActor)
	_ = os.MkdirAll(filepath.Join(e.dir, "gitea.json", "child"), 0o700)
	wantCode(t, e.err(e.m.Disconnect(testActor)), CodeStorageFailed)
}
