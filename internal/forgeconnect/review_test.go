package forgeconnect

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Findings from the independent review; each is listed in failures.md.

// Gitea names the granted scope in its refusal. A grant that was widened on
// the forge after sign-in must not be reported as limited.
func TestLimitCheckReadsGrantedScope(t *testing.T) {
	const gitea = "token does not have at least one of required scope(s), required=[read:organization], token scope="
	cases := []struct{ name, message, want string }{
		{"gitea read-only", gitea + "read:repository,read:user", AccessScoped},
		{"gitea public-only", gitea + "public-only,read:repository,read:user", AccessScoped},
		{"gitea widened", gitea + "write:repository,read:user", AccessFull},
		{"gitea all", gitea + "all", AccessFull},
		{"gitea sudo", gitea + "read:repository,sudo", AccessFull},
		{"gitea unknown word", gitea + "read:repository,owner", AccessFull},
		{"forgejo names no scope", "token does not have at least one of required scope(s): [read:user read:organization]", AccessScoped},
		{"empty message", "", AccessScoped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.forge.set(func(f *fakeForge) { f.orgsMessage = c.message })
			e.connect()
			if got := e.m.Status().Access; got != c.want {
				t.Fatalf("after sign-in access = %q, want %q", got, c.want)
			}
			if got := e.m.Check(e.ctx()).Access; got != c.want {
				t.Fatalf("after check access = %q, want %q", got, c.want)
			}
		})
	}
	// A refusal that is not JSON still counts as a refusal.
	if got := limitStatus(&reply{status: http.StatusForbidden, body: []byte("<html>" + upstreamSentinel)}); got != AccessScoped {
		t.Fatalf("non-JSON refusal = %q", got)
	}
}

// RFC 6749 section 6: a refresh reply without a refresh token keeps the old one.
func TestRefreshWithoutNewRefreshTokenKeepsOld(t *testing.T) {
	e := newEnv(t)
	e.connect()
	before := e.stored().RefreshToken
	e.forge.set(func(f *fakeForge) { f.refreshEmpty = true })
	e.clock.Advance(56 * time.Minute)
	if _, _, err := e.m.Fresh(e.ctx()); e.err(err) != nil {
		t.Fatal(err)
	}
	if got := e.stored().RefreshToken; got != before {
		t.Fatal("the stored refresh token was thrown away")
	}
	e.clock.Advance(56 * time.Minute)
	if _, _, err := e.m.Fresh(e.ctx()); e.err(err) != nil {
		t.Fatalf("the kept refresh token no longer works: %v", err)
	}
	if e.m.Status().State != StateConnected {
		t.Fatalf("state = %q", e.m.Status().State)
	}
}

// A caller that goes away mid-refresh must not lose a pair the forge has
// already rotated: with one-time refresh tokens that would end the connection.
func TestCancelledCallerDoesNotLoseRotatedTokens(t *testing.T) {
	e := newEnv(t)
	e.forge.set(func(f *fakeForge) { f.oneTime = true })
	e.connect()
	gate, seen := make(chan struct{}), make(chan struct{}, 2)
	e.forge.set(func(f *fakeForge) { f.refreshGate, f.refreshSeen = gate, seen })
	e.clock.Advance(56 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _, _, _ = e.m.Fresh(ctx) }()
	<-seen
	cancel()
	close(gate)
	<-done
	e.forge.set(func(f *fakeForge) { f.refreshGate, f.refreshSeen = nil, nil })
	e.forge.mu.Lock()
	current := e.forge.current
	e.forge.mu.Unlock()
	if rec := e.stored(); rec.AccessToken != current[0] || rec.RefreshToken != current[1] {
		t.Fatal("the rotated pair was not saved")
	}
	e.clock.Advance(56 * time.Minute)
	if _, _, err := e.m.Fresh(e.ctx()); e.err(err) != nil {
		t.Fatalf("next refresh failed: %v", err)
	}
}

// Cloud metadata reachable outside 169.254.0.0/16.
func TestGuardBlocksOtherMetadataAddresses(t *testing.T) {
	for _, blocked := range []string{
		"fd00:ec2::254",      // AWS IPv6 metadata
		"100.100.100.200",    // Alibaba metadata
		"64:ff9b::a9fe:a9fe", // 169.254.169.254 through NAT64
		"2002:a9fe:a9fe::1",  // 169.254.169.254 through 6to4
		"64:ff9b::",          // 0.0.0.0 through NAT64
	} {
		if !blockedAddr(netip.MustParseAddr(blocked)) {
			t.Errorf("%s is not blocked", blocked)
		}
		if controlAddress("tcp", netip.AddrPortFrom(netip.MustParseAddr(blocked), 443).String(), nil) == nil {
			t.Errorf("%s can be dialled", blocked)
		}
	}
	for _, allowed := range []string{
		"100.100.100.201", "100.64.0.1", // carrier-grade NAT and tailnets
		"fd00::1", "fd00:ec2::253", "10.0.0.1", "192.168.1.10", "127.0.0.1", "::1",
		"64:ff9b::a00:1", "2002:a00:1::1", // 10.0.0.1 through NAT64 and 6to4
	} {
		if blockedAddr(netip.MustParseAddr(allowed)) {
			t.Errorf("%s is blocked", allowed)
		}
	}
}

// A directory Helm did not create keeps its permissions; if others can reach
// it, Helm refuses to put the tokens there rather than silently closing it.
func TestStoreLeavesForeignDirectoryAlone(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	store, reason := newFileStore(shared), ""
	store.refused = func(why string) { reason = why }
	wantCode(t, store.save(sampleRecord()), CodeStorageFailed)
	if reason == "" {
		t.Fatal("the operator is not told why the directory was refused")
	}
	if info, err := os.Stat(shared); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("the shared directory's permissions were changed: %v", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(shared); len(entries) != 0 {
		t.Fatalf("files were left in a refused directory: %v", entries)
	}
	// An existing private directory is used as it is.
	private := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := newFileStore(private).save(sampleRecord()); err != nil {
		t.Fatal(err)
	}
}

// Operators link data directories; a symlinked state directory must work.
func TestStoreFollowsSymlinkedDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "volume")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "forge")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	s := newFileStore(link)
	if err := s.save(sampleRecord()); err != nil {
		t.Fatalf("save through a symlinked directory: %v", err)
	}
	if rec, broken := s.load(); broken || rec == nil {
		t.Fatal("the saved file does not load back")
	}
}

// A repository created while Helm pages through the list shifts another onto
// two pages. The list is keyed by name in the UI, so duplicates must go.
func TestRepositoriesAreDeduplicated(t *testing.T) {
	e := newEnv(t)
	e.connect()
	e.forge.set(func(f *fakeForge) { f.repos, f.repeatLast = 120, true })
	list, _, err := e.m.Repositories(e.ctx())
	if e.err(err) != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, repo := range list {
		if seen[repo.FullName] {
			t.Fatalf("%s is listed twice", repo.FullName)
		}
		seen[repo.FullName] = true
	}
	if len(list) != 120 {
		t.Fatalf("got %d repositories, want 120", len(list))
	}
}

// A stale or foreign callback must not cancel the sign-in in progress.
func TestWrongStateKeepsPendingSignIn(t *testing.T) {
	e := newEnv(t)
	code, state := e.forge.approve(e.begin(testActor))
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, "stale-"+fmt.Sprint(len(state)), "x", "", "")), CodeStateInvalid)
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, "", "x", "", "")), CodeStateInvalid)
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, "stale", "", "access_denied", "")), CodeStateInvalid)
	if err := e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")); err != nil {
		t.Fatalf("the real callback failed after a stale one: %v", err)
	}
	// The matching callback still consumes it.
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")), CodeStateInvalid)
}
