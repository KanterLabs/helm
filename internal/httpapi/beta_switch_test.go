package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/betaswitch"
	"github.com/KanterLabs/helm/internal/store"
)

const betaTestSHA = "0123456789abcdef0123456789abcdef01234567"

type betaSwitchClientStub struct {
	listCalls   int
	switchCalls int
	jobCalls    int
	job         betaswitch.Job
	list        betaswitch.ReleasesResponse
	err         error
}

func (c *betaSwitchClientStub) ListReleases(context.Context) (betaswitch.ReleasesResponse, error) {
	c.listCalls++
	return c.list, c.err
}

func (c *betaSwitchClientStub) RequestSwitch(_ context.Context, sha string) (betaswitch.Job, error) {
	c.switchCalls++
	job := c.job
	if job.TargetSHA == "" {
		job.TargetSHA = sha
	}
	return job, c.err
}

func (c *betaSwitchClientStub) GetJob(context.Context, string) (betaswitch.Job, error) {
	c.jobCalls++
	return c.job, c.err
}

func betaAdminIdentity() auth.Identity {
	return auth.Identity{Actor: store.Actor{ID: "beta-admin", Kind: "human", Admin: true}}
}

func betaRequestWithIdentity(method, target string, payload string, identity auth.Identity) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(payload))
	ctx := context.WithValue(req.Context(), requestIdentityKey, identity)
	return req.WithContext(ctx)
}

func serveBetaRoute(t *testing.T, server *Server, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	result := httptest.NewRecorder()
	server.route(result, req)
	return result
}

func TestBetaSwitchDisabledReturns404WithoutControllerCalls(t *testing.T) {
	server, _ := testServer(t, "disabled")
	client := &betaSwitchClientStub{}
	server.BetaSwitch = client
	for _, target := range []string{
		"/api/v1/admin/beta/builds",
		"/api/v1/admin/beta/switch",
		"/api/v1/admin/beta/switches/0123456789abcdef0123456789abcdef",
	} {
		method := http.MethodGet
		if strings.HasSuffix(target, "/switch") {
			method = http.MethodPost
		}
		request := httptest.NewRequest(method, target, nil)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("disabled %s status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
	if client.listCalls != 0 || client.switchCalls != 0 || client.jobCalls != 0 {
		t.Fatalf("disabled controller calls = list:%d switch:%d job:%d", client.listCalls, client.switchCalls, client.jobCalls)
	}
}

func TestBetaBuildsRequireAdminAndSanitizeResponse(t *testing.T) {
	server, _ := testServer(t, "tailnet")
	server.Cfg.BetaSwitchEnabled = true
	server.Cfg.PublicOrigin = "https://beta-helm.home.shanekanterman.dev"
	client := &betaSwitchClientStub{list: betaswitch.ReleasesResponse{
		CurrentSHA: betaTestSHA,
		Releases:   []betaswitch.Release{{SHA: betaTestSHA, Ref: "refs/heads/feature/foo", Current: true}},
	}}
	server.BetaSwitch = client

	response := serveBetaRoute(t, server, betaRequestWithIdentity(http.MethodGet, "/api/v1/admin/beta/builds", "", betaAdminIdentity()))
	if response.Code != http.StatusOK {
		t.Fatalf("builds status=%d body=%s", response.Code, response.Body.String())
	}
	var body betaBuildsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Enabled || body.CurrentSHA != betaTestSHA || len(body.Builds) != 1 || body.Builds[0].Ref != "refs/heads/feature/foo" || !body.Builds[0].Current {
		t.Fatalf("builds response=%+v", body)
	}

	for _, identity := range []auth.Identity{
		{Actor: store.Actor{ID: "nonadmin", Kind: "human"}},
		{Actor: store.Actor{ID: "agent", Kind: "agent", Admin: true}, IsToken: true},
	} {
		blocked := serveBetaRoute(t, server, betaRequestWithIdentity(http.MethodGet, "/api/v1/admin/beta/builds", "", identity))
		if blocked.Code != http.StatusForbidden {
			t.Fatalf("non-admin identity=%+v status=%d body=%s", identity, blocked.Code, blocked.Body.String())
		}
	}
	if client.listCalls != 1 {
		t.Fatalf("list calls=%d, want 1", client.listCalls)
	}
}

func TestBetaSwitchIdempotencyAndCSRF(t *testing.T) {
	server, _ := testServer(t, "tailnet")
	server.Cfg.BetaSwitchEnabled = true
	server.Cfg.PublicOrigin = "https://beta-helm.home.shanekanterman.dev"
	client := &betaSwitchClientStub{job: betaswitch.Job{ID: "abcdefabcdefabcdefabcdefabcdefab", State: betaswitch.JobQueued}}
	server.BetaSwitch = client
	identity := betaAdminIdentity()

	firstReq := betaRequestWithIdentity(http.MethodPost, "/api/v1/admin/beta/switch", `{"sha":"`+betaTestSHA+`"}`, identity)
	firstReq.Header.Set("Origin", server.Cfg.PublicOrigin)
	firstReq.Header.Set("Idempotency-Key", "switch-once")
	first := serveBetaRoute(t, server, firstReq)
	if first.Code != http.StatusAccepted || first.Header().Get("Location") != "/api/v1/admin/beta/switches/abcdefabcdefabcdefabcdefabcdefab" {
		t.Fatalf("first switch status=%d location=%q body=%s", first.Code, first.Header().Get("Location"), first.Body.String())
	}
	var accepted betaSwitchResponse
	if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if !accepted.Enabled || accepted.Job.ID == "" || accepted.Job.TargetSHA != betaTestSHA || accepted.Job.State != betaswitch.JobQueued {
		t.Fatalf("accepted response=%+v", accepted)
	}

	replayReq := betaRequestWithIdentity(http.MethodPost, "/api/v1/admin/beta/switch", `{"sha":"`+betaTestSHA+`"}`, identity)
	replayReq.Header.Set("Origin", server.Cfg.PublicOrigin)
	replayReq.Header.Set("Idempotency-Key", "switch-once")
	replay := serveBetaRoute(t, server, replayReq)
	if replay.Code != http.StatusAccepted || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay status=%d body=%s, want %s", replay.Code, replay.Body.String(), first.Body.String())
	}
	if client.switchCalls != 1 {
		t.Fatalf("switch calls after replay=%d, want 1", client.switchCalls)
	}

	conflictReq := betaRequestWithIdentity(http.MethodPost, "/api/v1/admin/beta/switch", `{"sha":"abcdefabcdefabcdefabcdefabcdefabcdefabcd"}`, identity)
	conflictReq.Header.Set("Origin", server.Cfg.PublicOrigin)
	conflictReq.Header.Set("Idempotency-Key", "switch-once")
	conflict := serveBetaRoute(t, server, conflictReq)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflicting target status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	if client.switchCalls != 1 {
		t.Fatalf("switch calls after conflict=%d, want 1", client.switchCalls)
	}

	missingOrigin := betaRequestWithIdentity(http.MethodPost, "/api/v1/admin/beta/switch", `{"sha":"`+betaTestSHA+`"}`, identity)
	missingOrigin.Header.Set("Idempotency-Key", "switch-no-origin")
	csrf := serveBetaRoute(t, server, missingOrigin)
	if csrf.Code != http.StatusForbidden {
		t.Fatalf("CSRF status=%d body=%s", csrf.Code, csrf.Body.String())
	}
}

func TestBetaSwitchStrictSHAAndUnknownFields(t *testing.T) {
	server, _ := testServer(t, "tailnet")
	server.Cfg.BetaSwitchEnabled = true
	server.Cfg.PublicOrigin = "https://beta-helm.home.shanekanterman.dev"
	client := &betaSwitchClientStub{job: betaswitch.Job{ID: "abcdefabcdefabcdefabcdefabcdefab", State: betaswitch.JobQueued}}
	server.BetaSwitch = client
	identity := betaAdminIdentity()
	for name, payload := range map[string]string{
		"uppercase": `{"sha":"0123456789ABCDEF0123456789ABCDEF01234567"}`,
		"short":     `{"sha":"0123456789abcdef"}`,
		"unknown":   `{"sha":"` + betaTestSHA + `","extra":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := betaRequestWithIdentity(http.MethodPost, "/api/v1/admin/beta/switch", payload, identity)
			req.Header.Set("Origin", server.Cfg.PublicOrigin)
			req.Header.Set("Idempotency-Key", "strict-"+name)
			response := serveBetaRoute(t, server, req)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("payload=%s status=%d body=%s", payload, response.Code, response.Body.String())
			}
		})
	}
	if client.switchCalls != 0 {
		t.Fatalf("invalid requests enqueued %d switches", client.switchCalls)
	}
}
