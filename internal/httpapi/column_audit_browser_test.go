package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

// TestColumnAuditBrowserHarness serves the real Helm router for the TC-310
// Playwright journeys. It is skipped unless HELM_COLUMN_AUDIT_BROWSER_ADDR names
// a loopback address. GitHub and the model are scripted; auth uses the normal
// disabled-mode synthetic human. Control routes exist only in this test binary.
//
//	HELM_COLUMN_AUDIT_BROWSER_ADDR=127.0.0.1:18310 \
//	HELM_COLUMN_AUDIT_VITE=http://127.0.0.1:5310   # or HELM_COLUMN_AUDIT_DIST=/abs/dist
//	go test ./internal/httpapi -run TestColumnAuditBrowserHarness -count=1 -timeout 60m
func TestColumnAuditBrowserHarness(t *testing.T) {
	addr := os.Getenv("HELM_COLUMN_AUDIT_BROWSER_ADDR")
	if addr == "" {
		t.Skip("set HELM_COLUMN_AUDIT_BROWSER_ADDR to serve the column-audit browser harness")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("only a literal loopback address is supported")
	}
	work := t.TempDir()
	token := filepath.Join(work, "github-token")
	if err = os.WriteFile(token, []byte(columnAuditToken), 0600); err != nil {
		t.Fatal(err)
	}
	upstream := &columnAuditUpstream{}
	remote := httptest.NewServer(upstream)
	defer remote.Close()
	h := &columnAuditHarness{upstream: upstream, remote: remote, ai: &columnAuditAI{upstream: upstream}, done: make(chan struct{}), cfg: config.Config{
		Addr: addr, DB: filepath.Join(work, "helm.db"), AuthMode: "disabled", PublicOrigin: "http://" + addr,
		GitHubEnabled: true, GitHubTokenFile: token, GitHubRepositories: []string{columnAuditRepository},
	}}
	if vite := os.Getenv("HELM_COLUMN_AUDIT_VITE"); vite != "" {
		target, err := url.Parse(vite)
		if err != nil {
			t.Fatal(err)
		}
		h.ui = httputil.NewSingleHostReverseProxy(target)
	} else if dist := os.Getenv("HELM_COLUMN_AUDIT_DIST"); dist != "" {
		h.ui = columnAuditStatic(dist)
	}
	if err = h.open(); err != nil {
		t.Fatal(err)
	}
	defer h.close()
	server := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		select {
		case <-h.done:
		case <-time.After(55 * time.Minute):
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	t.Logf("column-audit browser harness listening on %s", h.cfg.PublicOrigin)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}

type columnAuditHarness struct {
	mu       sync.RWMutex
	server   *Server
	data     *store.Store
	database *sql.DB
	actor    store.Actor
	cfg      config.Config
	upstream *columnAuditUpstream
	remote   *httptest.Server
	ai       *columnAuditAI
	ui       http.Handler
	done     chan struct{}
	stopOnce sync.Once
	dropMu   sync.Mutex
	dropNext bool
	seeds    int
}

func (h *columnAuditHarness) open() error {
	database, err := db.Open(context.Background(), h.cfg.DB)
	if err != nil {
		return err
	}
	data := store.New(database)
	actor, err := data.EnsureDisabledActor(context.Background())
	if err != nil {
		database.Close()
		return err
	}
	provider := githubprovider.New(githubprovider.Options{Enabled: true, TokenFile: h.cfg.GitHubTokenFile,
		Repositories: h.cfg.GitHubRepositories, Endpoint: h.remote.URL, HTTPClient: h.remote.Client()})
	s := New(data, auth.NewManager(data, h.cfg), h.cfg, h.ai)
	s.Github = NewGithubService(data, provider)
	s.GithubAudits = NewGithubAuditService(data, provider, h.ai, nil)
	s.StartGithubServices(context.Background())
	h.server, h.data, h.database, h.actor = s, data, database, actor
	return nil
}

func (h *columnAuditHarness) close() {
	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = h.server.StopGithubServices(ctx)
	}
	if h.database != nil {
		_ = h.database.Close()
	}
}

func (h *columnAuditHarness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/__columnaudit/") {
		h.control(w, r)
		return
	}
	api := strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/readyz" || r.URL.Path == "/healthz" || r.URL.Path == "/livez"
	if !api && h.ui != nil {
		// UI assets and the dev-server websocket never hold the restart lock.
		h.ui.ServeHTTP(w, r)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/apply") && h.takeDrop(r) {
		// Commit through the real handler, then lose the response at a gateway.
		// (A closed keep-alive socket is not used: browsers transparently re-send
		// the POST, which hides the unknown outcome this journey must show.)
		h.server.ServeHTTP(httptest.NewRecorder(), r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = io.WriteString(w, `{"error":{"code":"gateway_timeout","message":"The gateway timed out waiting for Helm"}}`)
		return
	}
	h.server.ServeHTTP(w, r)
}

// takeDrop consumes the one-shot transport loss for a final (non-preview) apply.
func (h *columnAuditHarness) takeDrop(r *http.Request) bool {
	h.dropMu.Lock()
	defer h.dropMu.Unlock()
	if !h.dropNext {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct {
		Preview *bool `json:"preview"`
	}
	if err != nil || json.Unmarshal(raw, &body) != nil || body.Preview == nil || *body.Preview {
		return false
	}
	h.dropNext = false
	return true
}

func (h *columnAuditHarness) control(w http.ResponseWriter, r *http.Request) {
	// Command-line callers have no Origin; cross-origin browser writes are denied.
	if origin := r.Header.Get("Origin"); origin != "" && origin != h.cfg.PublicOrigin {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/__columnaudit/") {
	case "seed":
		var input struct {
			Scenario string `json:"scenario"`
			Fillers  int    `json:"fillers"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input) != nil || input.Fillers < 0 || input.Fillers > 80 {
			http.Error(w, "invalid seed", 400)
			return
		}
		h.mu.Lock()
		h.seeds++
		suffix := strings.ToUpper(strings.TrimLeft(time.Now().UTC().Format("150405"), "0")) + string(rune('A'+h.seeds%26))
		out, err := seedColumnAuditProject(r.Context(), h.data, h.actor, input.Scenario, input.Fillers, suffix)
		h.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		columnAuditJSON(w, out)
	case "state":
		var input struct {
			Revoked   *bool `json:"revoked"`
			Advanced  *bool `json:"advanced"`
			DropNext  *bool `json:"drop_next_apply"`
			DelayMS   *int  `json:"delay_ms"`
			AIOffline *bool `json:"ai_offline"`
		}
		if r.Method == http.MethodPut && json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input) != nil {
			http.Error(w, "invalid state", 400)
			return
		}
		h.upstream.mu.Lock()
		if input.Revoked != nil {
			h.upstream.revoked = *input.Revoked
		}
		if input.Advanced != nil {
			h.upstream.advanced = *input.Advanced
		}
		if input.DelayMS != nil && *input.DelayMS >= 0 && *input.DelayMS <= 60000 {
			h.upstream.delayMS = *input.DelayMS
		}
		if input.AIOffline != nil {
			h.upstream.aiOffline = *input.AIOffline
		}
		state := map[string]any{"revoked": h.upstream.revoked, "advanced": h.upstream.advanced, "delay_ms": h.upstream.delayMS, "ai_offline": h.upstream.aiOffline, "upstream_requests": h.upstream.requests, "inference_calls": h.upstream.inference}
		h.upstream.mu.Unlock()
		h.dropMu.Lock()
		if input.DropNext != nil {
			h.dropNext = *input.DropNext
		}
		state["drop_next_apply"] = h.dropNext
		h.dropMu.Unlock()
		columnAuditJSON(w, state)
	case "restart":
		h.mu.Lock()
		defer h.mu.Unlock()
		h.close()
		// A real restart loses the process-local preview proof key.
		fresh := make([]byte, 32)
		_, _ = rand.Read(fresh)
		correctionProofKey = fresh
		if err := h.open(); err != nil {
			http.Error(w, "restart failed", 500)
			return
		}
		columnAuditJSON(w, map[string]bool{"restarted": true})
	case "db":
		h.mu.RLock()
		defer h.mu.RUnlock()
		out, err := columnAuditSnapshot(r.Context(), h.database, r.URL.Query().Get("project"))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		columnAuditJSON(w, out)
	case "stop":
		h.stopOnce.Do(func() { close(h.done) })
		columnAuditJSON(w, map[string]bool{"stopping": true})
	default:
		http.NotFound(w, r)
	}
}

// columnAuditSnapshot reads committed SQLite state directly, independent of the
// API under test. Confirmation proofs are never stored, so none can leak here.
func columnAuditSnapshot(ctx context.Context, database *sql.DB, projectID string) (map[string]any, error) {
	tasks := []map[string]any{}
	rows, err := database.QueryContext(ctx, `SELECT t.id,p.key||'-'||t.number,t.title,t.kind,c.name,c.semantic_state,t.version,t.completed_at,t.claimed_by,b.resolution
		FROM tasks t JOIN projects p ON p.id=t.project_id JOIN columns c ON c.id=t.column_id LEFT JOIN bug_details b ON b.task_id=t.id
		WHERE t.project_id=? AND t.deleted_at IS NULL ORDER BY t.number`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, key, title, kind, column, state string
		var version int64
		var completed, claimed, resolution sql.NullString
		if err = rows.Scan(&id, &key, &title, &kind, &column, &state, &version, &completed, &claimed, &resolution); err != nil {
			rows.Close()
			return nil, err
		}
		tasks = append(tasks, map[string]any{"id": id, "key": key, "title": title, "kind": kind, "column": column, "semantic_state": state, "version": version,
			"completed": completed.Valid, "claimed": claimed.Valid, "resolution": resolution.String})
	}
	rows.Close()
	events := []map[string]string{}
	rows, err = database.QueryContext(ctx, `SELECT COALESCE(p.key||'-'||t.number,''),e.type FROM events e LEFT JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=t.project_id WHERE e.project_id=? ORDER BY e.created_at,e.rowid`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, kind string
		if err = rows.Scan(&key, &kind); err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, map[string]string{"task": key, "type": kind})
	}
	rows.Close()
	receipts := []map[string]any{}
	rows, err = database.QueryContext(ctx, `SELECT p.key||'-'||t.number,r.action,r.outcome,r.request_key FROM github_audit_correction_receipts r JOIN tasks t ON t.id=r.task_id JOIN projects p ON p.id=t.project_id WHERE t.project_id=? ORDER BY r.created_at,r.rowid`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, action, outcome, request string
		if err = rows.Scan(&key, &action, &outcome, &request); err != nil {
			rows.Close()
			return nil, err
		}
		receipts = append(receipts, map[string]any{"task": key, "action": action, "outcome": outcome, "request_key": request})
	}
	rows.Close()
	var requests int
	if err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM github_audit_correction_requests q JOIN audit_runs a ON a.id=q.audit_id WHERE a.project_id=?`, projectID).Scan(&requests); err != nil {
		return nil, err
	}
	return map[string]any{"tasks": tasks, "events": events, "receipts": receipts, "correction_requests": requests}, nil
}

func columnAuditStatic(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(name); err != nil || info.IsDir() {
			if filepath.Ext(r.URL.Path) == "" {
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeFile(w, r, filepath.Join(dir, "index.html"))
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

func columnAuditJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}
