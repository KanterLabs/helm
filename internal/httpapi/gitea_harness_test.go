package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/forgeconnect"
	"github.com/KanterLabs/helm/internal/store"
)

// Sentinels: every secret the fake forge hands out, and every piece of
// upstream text, carries one of these so a leak can be found by scanning.
const (
	giteaAccessSentinel   = "GITEA-ACCESS-SENTINEL"
	giteaRefreshSentinel  = "GITEA-REFRESH-SENTINEL"
	giteaCodeSentinel     = "GITEA-CODE-SENTINEL"
	giteaUpstreamSentinel = "GITEA-UPSTREAM-SENTINEL"
	giteaClientID         = "helm-client-0123"
	giteaOrigin           = "http://helm.test"
	giteaPrefix           = "/api/v1/integrations/gitea"
)

// giteaFake is a compact loopback Gitea: discovery, version, the token
// endpoint with PKCE, the user, the limit check and repositories.
type giteaFake struct {
	t   *testing.T
	srv *httptest.Server

	mu           sync.Mutex
	calls        int
	issuer       string
	tokenError   [2]string // error, error_description for the next exchange
	n            int
	codes        map[string]string // code -> challenge
	access       map[string]bool
	repos        int
	issuedTokens []string
}

func newGiteaFake(t *testing.T) *giteaFake {
	f := &giteaFake{t: t, codes: map[string]string{}, access: map[string]bool{}, repos: 12}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *giteaFake) base() string { return f.srv.URL }

func (f *giteaFake) set(change func(*giteaFake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *giteaFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *giteaFake) secrets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{giteaAccessSentinel, giteaRefreshSentinel, giteaCodeSentinel, giteaUpstreamSentinel}, f.issuedTokens...)
}

// approve plays the forge's consent page: it checks the authorize URL and
// issues a code bound to its PKCE challenge.
func (f *giteaFake) approve(authorizeURL string) (code, state string) {
	f.t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil || !strings.HasPrefix(authorizeURL, f.base()+"/login/oauth/authorize?") {
		f.t.Fatalf("authorize URL outside the forge: %q", authorizeURL)
	}
	q := u.Query()
	if q.Get("redirect_uri") != giteaOrigin+giteaPrefix+"/oauth/callback" || q.Get("client_id") != giteaClientID || q.Get("state") == "" {
		f.t.Fatalf("authorize parameters wrong: %v", q)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	code = fmt.Sprintf("%s-%d", giteaCodeSentinel, f.n)
	f.codes[code] = q.Get("code_challenge")
	f.issuedTokens = append(f.issuedTokens, code, q.Get("state"))
	return code, q.Get("state")
}

func giteaWriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *giteaFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	switch path := r.URL.Path; {
	case path == "/.well-known/openid-configuration":
		issuer := f.issuer
		if issuer == "" {
			issuer = f.base()
		}
		giteaWriteJSON(w, http.StatusOK, map[string]any{"issuer": issuer, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}})
	case path == "/api/v1/version":
		giteaWriteJSON(w, http.StatusOK, map[string]string{"version": "28.3.0"})
	case path == "/login/oauth/access_token" && r.Method == http.MethodPost:
		_ = r.ParseForm()
		if f.tokenError[0] != "" {
			giteaWriteJSON(w, http.StatusBadRequest, map[string]string{"error": f.tokenError[0], "error_description": f.tokenError[1]})
			return
		}
		challenge, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			giteaWriteJSON(w, http.StatusBadRequest, map[string]string{"error": "unauthorized_client", "error_description": giteaUpstreamSentinel})
			return
		}
		f.n++
		access, refresh := fmt.Sprintf("%s-%d", giteaAccessSentinel, f.n), fmt.Sprintf("%s-%d", giteaRefreshSentinel, f.n)
		f.access[access] = true
		f.issuedTokens = append(f.issuedTokens, access, refresh)
		giteaWriteJSON(w, http.StatusOK, map[string]any{"access_token": access, "token_type": "bearer", "expires_in": 3600, "refresh_token": refresh})
	case strings.HasPrefix(path, "/api/v1/"):
		if !f.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] {
			giteaWriteJSON(w, http.StatusUnauthorized, map[string]string{"message": giteaUpstreamSentinel})
			return
		}
		switch path {
		case "/api/v1/user":
			giteaWriteJSON(w, http.StatusOK, map[string]any{"login": "helm-bot", "id": 7})
		case "/api/v1/user/orgs":
			giteaWriteJSON(w, http.StatusForbidden, map[string]string{"message": giteaUpstreamSentinel})
		case "/api/v1/user/repos":
			w.Header().Set("X-Total-Count", strconv.Itoa(f.repos))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			page = max(page, 1)
			list := []map[string]any{}
			for i := (page - 1) * limit; i < page*limit && i < f.repos; i++ {
				list = append(list, map[string]any{"full_name": fmt.Sprintf("team/repo-%02d", i), "private": i%2 == 0, "archived": false, "clone_url": giteaUpstreamSentinel})
			}
			giteaWriteJSON(w, http.StatusOK, list)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

// lockedBuffer captures the process log while a test runs.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// giteaHarness is one Helm server with two administrators, a member, an
// agent token and a fake forge. Every response it sees is kept and scanned
// for the forge's secrets when the test ends.
type giteaHarness struct {
	t                    *testing.T
	server               *Server
	data                 *store.Store
	forge                *giteaFake
	manager              *forgeconnect.Manager
	stateDir             string
	owner, admin, member string // session cookies
	ownerToken, agent    string // bearer tokens
	logs                 *lockedBuffer
	seen                 []string
}

func newGiteaHarness(t *testing.T, change ...func(*forgeconnect.Options)) *giteaHarness {
	t.Helper()
	logs := &lockedBuffer{}
	prior := log.Writer()
	log.SetOutput(io.MultiWriter(logs))
	t.Cleanup(func() { log.SetOutput(prior) })
	server, data := testServer(t, "local")
	h := &giteaHarness{t: t, server: server, data: data, forge: newGiteaFake(t), logs: logs, stateDir: t.TempDir() + "/forge"}
	ctx := context.Background()
	email := "owner@example.test"
	owner, err := data.CreateFirstAdmin(ctx, store.Actor{Kind: "human", Name: "Owner", Email: &email}, "owner-hash")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := data.CreateMember(ctx, store.Actor{Name: "Second admin", Email: stringPtr("admin@example.test")}, store.WorkspaceRoleAdmin, nil, "admin-hash", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	member, err := data.CreateMember(ctx, store.Actor{Name: "Member", Email: stringPtr("member@example.test")}, store.WorkspaceRoleMember, nil, "member-hash", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.owner, h.admin, h.member = "owner-session", "admin-session", "member-session"
	for cookie, id := range map[string]string{h.owner: owner.ID, h.admin: admin.ID, h.member: member.ID} {
		if err := data.CreateSession(ctx, id, cookie, time.Now().UTC().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, h.ownerToken, err = data.CreateToken(ctx, owner.ID, "owner token", []string{"projects:read", "projects:write"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	agent, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "build agent"}, owner.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, h.agent, err = data.CreateToken(ctx, agent.ID, "agent token", []string{"projects:read", "projects:write"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	h.manager = h.open(change...)
	t.Cleanup(h.scan)
	return h
}

// open builds a manager over the harness state directory, as a restart
// would, and installs it on the server.
func (h *giteaHarness) open(change ...func(*forgeconnect.Options)) *forgeconnect.Manager {
	h.t.Helper()
	options := forgeconnect.Options{Enabled: true, PublicOrigin: giteaOrigin, StateDir: h.stateDir}
	for _, c := range change {
		c(&options)
	}
	m, err := forgeconnect.New(options)
	if err != nil {
		h.t.Fatalf("forgeconnect.New: %v", err)
	}
	h.t.Cleanup(m.Close)
	h.server.Gitea = m
	return m
}

type giteaCaller struct {
	cookie, bearer string
	noOrigin       bool
	headers        map[string]string
}

// call sends one request through ServeHTTP and checks the no-store rule.
func (h *giteaHarness) call(as giteaCaller, method, path string, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, reader)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if as.cookie != "" {
		r.AddCookie(&http.Cookie{Name: "roadmap_session", Value: as.cookie})
	}
	if as.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+as.bearer)
	}
	if method != http.MethodGet && !as.noOrigin {
		r.Header.Set("Origin", giteaOrigin)
	}
	for name, value := range as.headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	h.server.ServeHTTP(w, r)
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		h.t.Errorf("%s %s: Cache-Control = %q", method, path, got)
	}
	h.seen = append(h.seen, w.Body.String(), w.Header().Get("Location"))
	return w
}

func (h *giteaHarness) as(cookie string) giteaCaller { return giteaCaller{cookie: cookie} }

// connect runs the whole sign-in as the owner through the HTTP routes.
func (h *giteaHarness) connect() {
	h.t.Helper()
	authorize := h.start(h.owner)
	code, state := h.forge.approve(authorize)
	w := h.call(h.as(h.owner), http.MethodGet, giteaPrefix+"/oauth/callback?"+url.Values{"code": {code}, "state": {state}}.Encode(), "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings?gitea=connected" {
		h.t.Fatalf("callback = %d %q", w.Code, w.Header().Get("Location"))
	}
}

func (h *giteaHarness) start(cookie string) string {
	h.t.Helper()
	w := h.call(h.as(cookie), http.MethodPost, giteaPrefix+"/oauth/start", fmt.Sprintf(`{"base_url":%q,"client_id":%q,"allow_insecure_http":true}`, h.forge.base(), giteaClientID))
	var out struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.AuthorizeURL == "" {
		h.t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	h.seen = h.seen[:len(h.seen)-2] // the authorize URL carries the state by design
	return out.AuthorizeURL
}

func (h *giteaHarness) scan() {
	h.t.Helper()
	haystacks := append([]string{h.logs.String()}, h.seen...)
	for _, secret := range h.forge.secrets() {
		for _, text := range haystacks {
			if secret != "" && strings.Contains(text, secret) {
				h.t.Errorf("secret %.24q… leaked into %.200q", secret, text)
			}
		}
	}
}

func decodeGitea(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %d %q: %v", w.Code, w.Body.String(), err)
	}
	return out
}

func giteaErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	body := decodeGitea(t, w)
	envelope, _ := body["error"].(map[string]any)
	code, _ := envelope["code"].(string)
	return code
}
