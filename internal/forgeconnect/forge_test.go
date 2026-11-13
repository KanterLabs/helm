package forgeconnect

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Sentinels: every secret the fake forge hands out carries one of these, so a
// leak into a log line, an error string or a status document can be found.
const (
	accessSentinel   = "ACCESS-SENTINEL"
	refreshSentinel  = "REFRESH-SENTINEL"
	codeSentinel     = "CODE-SENTINEL"
	upstreamSentinel = "UPSTREAM-SENTINEL"
	testClientID     = "helm-client-0123"
	testOrigin       = "https://helm.example"
	testActor        = "admin-1"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type issuedCode struct {
	challenge   string
	redirectURI string
	clientID    string
}

// fakeForge behaves like the Gitea 28.3.0 and Forgejo 17.0.1 tables in
// docs/GITEA_OAUTH_RESEARCH.md.
type fakeForge struct {
	t      *testing.T
	srv    *httptest.Server
	prefix string

	mu            sync.Mutex
	clientID      string
	confidential  bool
	appDeleted    bool
	revoked       bool
	oneTime       bool
	sameBytes     bool
	refreshEmpty  bool
	issuer        string // overrides the advertised issuer when set
	grants        []string
	methods       []string
	version       string
	versionStatus int
	expiresIn     any
	tokenType     string
	tokenOverride string // raw JSON body for the next token response
	tokenStatus   int    // status for every token response when set
	orgsStatus    int
	orgsMessage   string // refusal text for the limit check when set
	repeatLast    bool   // each repository page starts with the previous page's last item
	userStatus    int
	reposStatus   int
	repos         int
	repoBody      string // raw body for repository pages when set
	totalHeader   *string
	refreshGate   chan struct{}
	refreshSeen   chan struct{}
	codes         map[string]issuedCode
	access        map[string]bool
	refresh       map[string]bool
	used          map[string]bool
	current       [2]string
	n             int
	calls         map[string]int
	refreshCalls  int
	states        []string
	verifiers     []string
	lastRedirect  string
	redirectTo    string // when set, every request is redirected there
}

func newFakeForge(t *testing.T, prefix string) *fakeForge {
	f := &fakeForge{
		t: t, prefix: prefix, clientID: testClientID,
		grants:  []string{"authorization_code", "refresh_token"},
		methods: []string{"plain", "S256"}, version: "28.3.0", versionStatus: 200,
		expiresIn: 3600, tokenType: "bearer", orgsStatus: 403, repos: 12,
		codes: map[string]issuedCode{}, access: map[string]bool{}, refresh: map[string]bool{},
		used: map[string]bool{}, calls: map[string]int{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeForge) base() string { return f.srv.URL + f.prefix }

func (f *fakeForge) set(change func(*fakeForge)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeForge) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

func (f *fakeForge) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.calls {
		total += n
	}
	return total
}

func (f *fakeForge) refreshes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshCalls
}

// secrets lists every secret value this forge saw or issued.
func (f *fakeForge) secrets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{accessSentinel, refreshSentinel, codeSentinel, upstreamSentinel}
	out = append(out, f.states...)
	return append(out, f.verifiers...)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func forgeTokenError(w http.ResponseWriter, code, description string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code, "error_description": description + " " + upstreamSentinel})
}

// approve plays the browser and the consent page: it validates the authorize
// URL the way Gitea does and returns the code it would redirect back with.
func (f *fakeForge) approve(authorizeURL string) (code, state string) {
	f.t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil || !strings.HasPrefix(authorizeURL, f.base()+"/login/oauth/authorize?") {
		f.t.Fatalf("authorize URL outside the forge")
	}
	q := u.Query()
	if len(q) != 7 || q.Get("client_id") != f.clientID || q.Get("response_type") != "code" ||
		q.Get("code_challenge_method") != "S256" || q.Get("scope") != "read:repository read:user" ||
		q.Get("redirect_uri") != testOrigin+"/api/v1/integrations/gitea/oauth/callback" || q.Get("state") == "" || q.Get("code_challenge") == "" {
		f.t.Fatalf("authorize parameters wrong: %v", q)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	code = fmt.Sprintf("%s-%d", codeSentinel, f.n)
	f.codes[code] = issuedCode{challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri"), clientID: q.Get("client_id")}
	f.states = append(f.states, q.Get("state"))
	return code, q.Get("state")
}

func (f *fakeForge) issueLocked(w http.ResponseWriter) {
	if f.tokenOverride != "" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.tokenOverride))
		return
	}
	f.n++
	access, refresh := fmt.Sprintf("%s-%d", accessSentinel, f.n), fmt.Sprintf("%s-%d", refreshSentinel, f.n)
	if f.sameBytes && f.current[0] != "" {
		access, refresh = f.current[0], f.current[1]
	}
	f.access[access], f.refresh[refresh] = true, true
	f.current = [2]string{access, refresh}
	body := map[string]any{"access_token": access, "token_type": f.tokenType, "expires_in": f.expiresIn, "refresh_token": refresh}
	if f.refreshEmpty {
		body["refresh_token"] = ""
	}
	writeJSON(w, http.StatusOK, body)
}

// expireAccess makes every issued access token answer 401, as Gitea does
// after expiry.
func (f *fakeForge) expireAccess() {
	f.set(func(f *fakeForge) { f.access = map[string]bool{} })
}

func (f *fakeForge) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, f.prefix)
	if f.prefix != "" && !strings.HasPrefix(r.URL.Path, f.prefix+"/") {
		path = "!outside"
	}
	f.mu.Lock()
	f.calls[path]++
	redirect := f.redirectTo
	f.mu.Unlock()
	if redirect != "" {
		http.Redirect(w, r, redirect+r.URL.Path, http.StatusTemporaryRedirect)
		return
	}
	switch {
	case path == "/.well-known/openid-configuration" && r.Method == http.MethodGet:
		f.mu.Lock()
		issuer := f.issuer
		if issuer == "" {
			issuer = f.base()
		}
		doc := map[string]any{"issuer": issuer, "authorization_endpoint": f.base() + "/login/oauth/authorize",
			"token_endpoint": f.base() + "/login/oauth/access_token", "grant_types_supported": f.grants,
			"code_challenge_methods_supported": f.methods, "response_types_supported": []string{"code", "id_token"}}
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, doc)
	case path == "/api/v1/version":
		f.mu.Lock()
		status, version := f.versionStatus, f.version
		f.mu.Unlock()
		if status != http.StatusOK {
			writeJSON(w, status, map[string]string{"message": "token is required " + upstreamSentinel})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"version": version})
	case path == "/login/oauth/access_token" && r.Method == http.MethodPost:
		f.token(w, r)
	case strings.HasPrefix(path, "/api/v1/"):
		f.api(w, r, path)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeForge) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		forgeTokenError(w, "invalid_request", "bad form")
		return
	}
	form := r.PostForm
	if form.Get("client_secret") != "" {
		f.t.Errorf("a client secret was sent")
	}
	if form.Get("grant_type") == "refresh_token" {
		f.mu.Lock()
		f.refreshCalls++
		gate, seen := f.refreshGate, f.refreshSeen
		f.mu.Unlock()
		if seen != nil {
			seen <- struct{}{}
		}
		if gate != nil {
			<-gate
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokenStatus != 0 {
		w.WriteHeader(f.tokenStatus)
		_, _ = w.Write([]byte("<html>" + upstreamSentinel + "</html>"))
		return
	}
	switch form.Get("grant_type") {
	case "authorization_code":
		if f.confidential {
			forgeTokenError(w, "unauthorized_client", "invalid empty client secret")
			return
		}
		if f.appDeleted || form.Get("client_id") != f.clientID {
			forgeTokenError(w, "invalid_client", "cannot load client with client id: '"+form.Get("client_id")+"'")
			return
		}
		issued, ok := f.codes[form.Get("code")]
		delete(f.codes, form.Get("code"))
		if !ok {
			forgeTokenError(w, "unauthorized_client", "client is not authorized")
			return
		}
		f.verifiers = append(f.verifiers, form.Get("code_verifier"))
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != issued.challenge {
			forgeTokenError(w, "unauthorized_client", "failed PKCE code challenge")
			return
		}
		f.lastRedirect = form.Get("redirect_uri")
		if form.Get("redirect_uri") != issued.redirectURI {
			forgeTokenError(w, "unauthorized_client", "unexpected redirect URI")
			return
		}
		f.issueLocked(w)
	case "refresh_token":
		switch {
		case f.confidential:
			forgeTokenError(w, "invalid_client", "invalid empty client secret")
		case f.appDeleted || form.Get("client_id") != f.clientID:
			forgeTokenError(w, "invalid_client", "cannot load client with client id: '"+form.Get("client_id")+"'")
		case f.revoked:
			forgeTokenError(w, "invalid_grant", "grant does not exist")
		case !f.refresh[form.Get("refresh_token")]:
			forgeTokenError(w, "unauthorized_client", "unable to parse refresh token")
		case f.oneTime && f.used[form.Get("refresh_token")]:
			forgeTokenError(w, "unauthorized_client", "token was already used")
		default:
			if f.oneTime {
				f.used[form.Get("refresh_token")] = true
			}
			f.issueLocked(w)
		}
	default:
		forgeTokenError(w, "unsupported_grant_type", "Only refresh_token or authorization_code grant type is supported")
	}
}

func (f *fakeForge) api(w http.ResponseWriter, r *http.Request, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") == "" && path != "/api/v1/version" {
		f.t.Errorf("API call without a bearer token: %s", path)
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if f.revoked || !f.access[token] {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "invalid username, password or token " + upstreamSentinel})
		return
	}
	switch path {
	case "/api/v1/user":
		if f.userStatus != 0 {
			writeJSON(w, f.userStatus, map[string]string{"message": upstreamSentinel})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"login": "helm-bot", "id": 7, "email": "bot@example.invalid"})
	case "/api/v1/user/orgs":
		if f.orgsStatus == http.StatusOK {
			writeJSON(w, http.StatusOK, []any{})
			return
		}
		message := "token does not have at least one of required scope(s) " + upstreamSentinel
		if f.orgsMessage != "" {
			message = f.orgsMessage
		}
		writeJSON(w, f.orgsStatus, map[string]string{"message": message})
	case "/api/v1/user/repos":
		if f.reposStatus != 0 {
			writeJSON(w, f.reposStatus, map[string]string{"message": upstreamSentinel})
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		if limit < 1 || limit > 50 {
			limit = 50
		}
		if f.totalHeader != nil {
			w.Header().Set("X-Total-Count", *f.totalHeader)
		} else {
			w.Header().Set("X-Total-Count", strconv.Itoa(f.repos))
		}
		if f.repoBody != "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(f.repoBody))
			return
		}
		list := []map[string]any{}
		start := (page - 1) * limit
		if f.repeatLast {
			start -= page - 1
		}
		for i := start; i < start+limit && i < f.repos; i++ {
			list = append(list, map[string]any{"full_name": fmt.Sprintf("team/repo-%03d", i), "private": i%2 == 0, "archived": i%5 == 0})
		}
		writeJSON(w, http.StatusOK, list)
	default:
		http.NotFound(w, r)
	}
}
