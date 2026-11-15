package githubconnect

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConnectEndToEnd(t *testing.T) {
	e := newEnv(t)
	got := e.m.Status(testActor)
	if got.Mode != ModeDeviceFlow || !got.SigninAvailable || got.State != StateDisconnected || got.Attempt != nil {
		t.Fatalf("initial status %+v", got)
	}
	var mu sync.Mutex
	var repos [][]string
	e.m.OnRepositories(func(list []string) {
		mu.Lock()
		defer mu.Unlock()
		repos = append(repos, list)
	})
	e.connect()
	s := e.m.Status(testActor)
	if s.Login != "octocat" || s.ConnectedAt == "" || s.Attempt != nil || s.RepositoriesTruncated {
		t.Fatalf("connected status %+v", s)
	}
	if s.InstallURL != "https://github.com/apps/kanterlabs-helm/installations/new" || s.AuthorizationsURL != "https://github.com/settings/apps/authorizations" {
		t.Fatalf("links %q %q", s.InstallURL, s.AuthorizationsURL)
	}
	if want := []string{"kanterlabs/docs", "kanterlabs/helm"}; strings.Join(e.m.Repositories(), ",") != strings.Join(want, ",") {
		t.Fatalf("repositories %v", e.m.Repositories())
	}
	waitFor(t, "the listener", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(repos) >= 2 && strings.Join(repos[len(repos)-1], ",") == "kanterlabs/docs,kanterlabs/helm"
	})
	token, identity, err := e.m.Current()
	if e.err(err) != nil || !strings.Contains(token, accessSentinel) || identity == "" {
		t.Fatalf("Current: %v", err)
	}
	if forms := e.gh.count(func(g *fakeGitHub) int { return len(g.deviceForm) }); forms != 1 {
		t.Fatalf("device calls %d", forms)
	}
	var sent string
	e.gh.set(func(g *fakeGitHub) { sent = g.deviceForm[0] })
	if sent != testClientID {
		t.Fatalf("client_id sent %q", sent)
	}
}

func TestStatusJSONMatchesTheWebType(t *testing.T) {
	e := newEnv(t)
	shape := func(s Status) map[string]any {
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		return out
	}
	idle := shape(e.m.Status(testActor))
	for _, key := range []string{"attempt", "login", "connected_at", "user_code"} {
		if _, ok := idle[key]; ok {
			t.Fatalf("disconnected status carries %q: %v", key, idle)
		}
	}
	for _, key := range []string{"mode", "signin_available", "state", "install_url", "authorizations_url", "repositories_truncated"} {
		if _, ok := idle[key]; !ok {
			t.Fatalf("status lacks %q: %v", key, idle)
		}
	}
	e.begin(testActor)
	mine := shape(e.m.Status(testActor))["attempt"].(map[string]any)
	for _, key := range []string{"id", "state", "user_code", "verification_uri", "expires_at", "message"} {
		if _, ok := mine[key]; !ok {
			t.Fatalf("attempt lacks %q: %v", key, mine)
		}
	}
	if mine["verification_uri"] != e.gh.URL+"/login/device" || mine["state"] != AttemptPending {
		t.Fatalf("attempt %v", mine)
	}
}

func TestUserCodeOnlyForTheBeginner(t *testing.T) {
	e := newEnv(t)
	e.begin(testActor)
	if s := e.m.Status(testActor); s.Attempt == nil || s.Attempt.UserCode != userCode {
		t.Fatalf("beginner status %+v", s.Attempt)
	}
	other := e.m.Status(otherActor)
	if other.Attempt == nil || other.Attempt.UserCode != "" {
		t.Fatalf("other administrator sees %+v", other.Attempt)
	}
	raw, _ := json.Marshal(other)
	if strings.Contains(string(raw), "user_code") || strings.Contains(string(raw), userCode) {
		t.Fatalf("other administrator's JSON carries the code: %s", raw)
	}
	if e.m.Status("").Attempt.UserCode != "" {
		t.Fatal("an empty actor sees the code")
	}
	first := e.m.Status(testActor).Attempt.ID
	replaced := e.begin(otherActor)
	if replaced.Attempt.ID == first || replaced.Attempt.UserCode != userCode {
		t.Fatalf("another administrator did not replace the attempt: %+v", replaced.Attempt)
	}
	if e.m.Status(testActor).Attempt.UserCode != "" {
		t.Fatal("the replaced administrator still sees a code")
	}
}

func TestSameActorGetsThePendingAttempt(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	ids := make([]string, 4)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := e.m.Begin(e.ctx(), testActor)
			if e.err(err) == nil {
				ids[i] = s.Attempt.ID
			}
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id == "" || id != ids[0] {
			t.Fatalf("attempt ids %v", ids)
		}
	}
	if calls := e.gh.count(func(g *fakeGitHub) int { return g.deviceCalls }); calls != 1 {
		t.Fatalf("device codes requested %d times", calls)
	}
}

func TestBeginRefusals(t *testing.T) {
	off := newEnv(t, func(o *Options) { o.ClientID = "" })
	if s := off.m.Status(testActor); s.SigninAvailable {
		t.Fatal("sign-in available without a client ID")
	}
	_, err := off.m.Begin(off.ctx(), testActor)
	wantCode(t, off.err(err), CodeSigninUnavailable)

	e := newEnv(t)
	_, err = e.m.Begin(e.ctx(), "")
	wantCode(t, e.err(err), CodeInvalidRequest)
	e.connect()
	_, err = e.m.Begin(e.ctx(), testActor)
	wantCode(t, e.err(err), CodeAlreadyConnected)
	e.m.Close()
	_, err = e.m.Begin(e.ctx(), testActor)
	wantCode(t, e.err(err), CodeSigninUnavailable)
}

func TestCancelWithStaleIDKeepsTheAttempt(t *testing.T) {
	e := newEnv(t)
	s := e.begin(testActor)
	after := e.m.Cancel(testActor, "stale-id")
	if after.Attempt == nil || after.Attempt.ID != s.Attempt.ID {
		t.Fatalf("stale cancel removed the attempt: %+v", after.Attempt)
	}
	if got := e.m.Cancel(testActor, s.Attempt.ID); got.Attempt != nil {
		t.Fatalf("cancel kept %+v", got.Attempt)
	}
}

func TestCancelStopsPolling(t *testing.T) {
	e := newEnv(t)
	e.start()
	s := e.begin(testActor)
	e.step()
	e.m.Cancel(testActor, s.Attempt.ID)
	e.quiet()
}

func TestExpiredAttemptStopsPolling(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.begin(testActor)
	e.step()
	e.clock.advance(901 * time.Second)
	waitFor(t, "a poll timer", e.hasPending)
	e.timers.fire()
	waitFor(t, "expiry", func() bool {
		s := e.m.Status(testActor)
		return s.Attempt != nil && s.Attempt.State == AttemptExpired
	})
	if msg := e.m.Status(testActor).Attempt.Message; msg == "" {
		t.Fatal("expired attempt has no message")
	}
	e.quiet()
}

func TestDisconnectStopsPolling(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.begin(testActor)
	e.step()
	if err := e.err(e.m.Disconnect(testActor)); err != nil {
		t.Fatal(err)
	}
	if s := e.m.Status(testActor); s.Attempt != nil {
		t.Fatalf("attempt survived disconnect: %+v", s.Attempt)
	}
	e.quiet()
}

func TestCloseStopsPolling(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.begin(testActor)
	e.step()
	e.m.Close()
	e.m.Close()
	e.quiet()
	e.start() // a closed manager never starts again
	e.quiet()
}

func (e *env) currentAttempt() *attempt {
	e.m.mu.Lock()
	defer e.m.mu.Unlock()
	return e.m.attempt
}

// pollDuring runs one poll of the current attempt and calls during while
// GitHub is answering it (with success).
func (e *env) pollDuring(during func()) {
	e.t.Helper()
	att := e.currentAttempt()
	gate := make(chan struct{})
	e.gh.set(func(g *fakeGitHub) { g.pollScript, g.pollGate = []string{"success"}, gate })
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.m.pollOnce(e.ctx(), att)
	}()
	waitFor(e.t, "the poll to reach GitHub", func() bool { return e.gh.count(func(g *fakeGitHub) int { return g.polls }) == 1 })
	during()
	close(gate)
	<-done
}

func TestReplacedAttemptSuccessIsDiscarded(t *testing.T) {
	e := newEnv(t)
	e.begin(testActor)
	e.pollDuring(func() { e.begin(otherActor) })
	s := e.m.Status(otherActor)
	if s.State != StateDisconnected || s.Attempt == nil || s.Attempt.State != AttemptPending || s.Attempt.UserCode == "" {
		t.Fatalf("late success of a replaced attempt changed state: %+v", s)
	}
}

func TestSuccessAfterCancelIsDiscarded(t *testing.T) {
	e := newEnv(t)
	s := e.begin(testActor)
	e.pollDuring(func() { e.m.Cancel(testActor, s.Attempt.ID) })
	if got := e.m.Status(testActor); got.State != StateDisconnected || got.Attempt != nil {
		t.Fatalf("success after cancel connected: %+v", got)
	}
}

func TestSuccessAfterDisconnectIsDiscarded(t *testing.T) {
	e := newEnv(t)
	e.begin(testActor)
	e.pollDuring(func() { must(t, e.err(e.m.Disconnect(testActor))) })
	if got := e.m.Status(testActor); got.State != StateDisconnected || got.Attempt != nil {
		t.Fatalf("success after disconnect connected: %+v", got)
	}
}

func TestSlowDownLengthensTheInterval(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.gh.set(func(g *fakeGitHub) { g.pollScript = []string{"slow_down", "authorization_pending", "slow_down"} })
	e.begin(testActor)
	waitFor(t, "first wait", func() bool { return e.timers.count() > 0 })
	if got := e.timers.last(); got != 5*time.Second {
		t.Fatalf("first wait %v", got)
	}
	for _, want := range []time.Duration{10 * time.Second, 10 * time.Second, 15 * time.Second} {
		e.step()
		if got := e.timers.last(); got != want {
			t.Fatalf("wait %v, want %v", got, want)
		}
	}
}

func TestPollOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		script []string
		user   string
		state  string
		code   string
	}{
		{"expired", []string{"expired_token"}, "", AttemptExpired, "expired"},
		{"denied", []string{"access_denied"}, "", AttemptDenied, "denied"},
		{"unknown error", []string{"incorrect_client_credentials"}, "", AttemptFailed, CodeGitHubRefused},
		{"unreachable", []string{"500", "500", "garbage", "500", "500"}, "", AttemptFailed, CodeGitHubUnreachable},
		{"user lookup", []string{"success"}, `{"login":"","id":0}`, AttemptFailed, CodeGitHubUnreachable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.start()
			e.gh.set(func(g *fakeGitHub) { g.pollScript, g.user = c.script, c.user })
			e.begin(testActor)
			for range c.script {
				e.step()
			}
			s := e.m.Status(testActor)
			if s.State != StateDisconnected || s.Attempt == nil || s.Attempt.State != c.state || s.Attempt.Message == "" {
				t.Fatalf("status %+v / %+v", s, s.Attempt)
			}
			if strings.Contains(s.Attempt.Message, "upstream") {
				t.Fatalf("message carries GitHub text: %q", s.Attempt.Message)
			}
			want := `{"level":"warn","msg":"github sign-in failed","code":"` + c.code + `"}`
			if logs := e.logLines(); len(logs) != 1 || logs[0] != want {
				t.Fatalf("logs %q, want one %q", logs, want)
			}
			e.quiet()
		})
	}
}

func TestDeviceCodeErrorIn200(t *testing.T) {
	e := newEnv(t)
	e.gh.set(func(g *fakeGitHub) {
		g.deviceReply = `{"error":"device_flow_disabled","error_description":"nope ` + deviceSentinel + `"}`
	})
	_, err := e.m.Begin(e.ctx(), testActor)
	wantCode(t, e.err(err), CodeGitHubRefused)
	if s := e.m.Status(testActor); s.Attempt != nil {
		t.Fatalf("attempt after refusal: %+v", s.Attempt)
	}
}

func TestDeviceCodeReplyIsValidated(t *testing.T) {
	good := `"device_code":"` + deviceSentinel + `1","user_code":"` + userCode + `","verification_uri":"{web}/login/device"`
	bad := map[string]string{
		"foreign uri":     `{"device_code":"` + deviceSentinel + `1","user_code":"` + userCode + `","verification_uri":"https://evil.example/login/device","expires_in":900,"interval":5}`,
		"zero expiry":     `{` + good + `,"expires_in":0,"interval":5}`,
		"absurd expiry":   `{` + good + `,"expires_in":99999999,"interval":5}`,
		"absurd interval": `{` + good + `,"expires_in":900,"interval":3600}`,
		"negative":        `{` + good + `,"expires_in":900,"interval":-1}`,
		"bad user code":   `{"device_code":"` + deviceSentinel + `1","user_code":"<b>x</b>","verification_uri":"{web}/login/device","expires_in":900}`,
		"spaced device":   `{"device_code":"` + deviceSentinel + ` 1","user_code":"` + userCode + `","verification_uri":"{web}/login/device","expires_in":900}`,
		"no device code":  `{"user_code":"` + userCode + `","verification_uri":"{web}/login/device","expires_in":900}`,
		"truncated":       `{` + good,
		"not json":        `<html>` + deviceSentinel + `</html>`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.gh.set(func(g *fakeGitHub) { g.deviceReply = body })
			_, err := e.m.Begin(e.ctx(), testActor)
			wantCode(t, e.err(err), CodeGitHubUnreachable)
		})
	}
	for body, want := range map[string]time.Duration{
		`{` + good + `,"expires_in":900}`:              5 * time.Second,
		`{` + good + `,"expires_in":900,"interval":1}`: 5 * time.Second,
		`{` + good + `,"expires_in":900,"interval":9}`: 9 * time.Second,
	} {
		e := newEnv(t)
		e.start()
		e.gh.set(func(g *fakeGitHub) { g.deviceReply = body })
		e.begin(testActor)
		waitFor(t, "first wait", func() bool { return e.timers.count() > 0 })
		if got := e.timers.last(); got != want {
			t.Fatalf("%s: first wait %v, want %v", body, got, want)
		}
	}
}

func TestFailedSignInIsLoggedOnce(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.gh.set(func(g *fakeGitHub) { g.pollScript = []string{"access_denied"} })
	e.begin(testActor)
	e.step()
	e.quiet()
	e.m.Status(testActor)
	e.gh.set(func(g *fakeGitHub) { g.deviceStatus, g.deviceReply = 500, "oops" })
	_, err := e.m.Begin(e.ctx(), testActor)
	wantCode(t, e.err(err), CodeGitHubUnreachable)
	want := []string{
		`{"level":"warn","msg":"github sign-in failed","code":"denied"}`,
		`{"level":"warn","msg":"github sign-in failed","code":"github_unreachable"}`,
	}
	if got := e.logLines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("logs:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
