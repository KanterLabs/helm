package forgeconnect

import (
	"strings"
	"testing"
)

// A failed sign-in must be visible to an operator, by code only. The sentinel
// scan that runs after every test proves the upstream text stays out.
func TestFailedSignInIsLogged(t *testing.T) {
	e := newEnv(t)
	_, state := e.forge.approve(e.begin(testActor))
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, "", "access_denied", "denied "+upstreamSentinel)), CodeDenied)
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, "forged", "", "", "")), CodeStateInvalid)
	e.mu.Lock()
	logs := strings.Join(e.logs, "\n")
	e.mu.Unlock()
	for _, code := range []string{CodeDenied, CodeStateInvalid} {
		if want := `"msg":"gitea sign-in failed","code":"` + code + `"`; !strings.Contains(logs, want) {
			t.Errorf("no log line %s in:\n%s", want, logs)
		}
	}
}
