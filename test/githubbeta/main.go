// githubbeta serves the real application against a synthetic GitHub upstream.
// This executable is never built into Helm's production entrypoint.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/httpapi"
	"github.com/KanterLabs/helm/internal/store"
)

type harness struct {
	mu       sync.RWMutex
	server   *httpapi.Server
	database *sql.DB
	cfg      config.Config
	upstream *httptest.Server
	state    *syntheticState
	ai       *syntheticAI
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18082", "loopback listen address")
	work := flag.String("work-dir", "", "protected temporary working directory")
	flag.Parse()
	if *work == "" {
		log.Fatal("-work-dir is required")
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("only a literal loopback listen address is supported")
	}
	if err = os.MkdirAll(*work, 0700); err != nil {
		log.Fatal("cannot create work directory")
	}
	if err = os.Chmod(*work, 0700); err != nil {
		log.Fatal("cannot protect work directory")
	}
	tokenFile := filepath.Join(*work, "github-token")
	if err = os.WriteFile(tokenFile, []byte(sentinelToken), 0600); err != nil {
		log.Fatal("cannot create synthetic credential")
	}
	defer os.Remove(tokenFile)
	state := &syntheticState{}
	upstream := httptest.NewServer(state)
	defer upstream.Close()
	h := &harness{state: state, upstream: upstream, ai: &syntheticAI{state: state}, cfg: config.Config{
		Addr: *addr, DB: filepath.Join(*work, "helm.db"), AuthMode: "disabled", PublicOrigin: "http://" + *addr,
		GitHubEnabled: true, GitHubTokenFile: tokenFile, GitHubRepositories: []string{syntheticRepository},
	}}
	if err = h.open(); err != nil {
		log.Fatal("cannot initialize synthetic Helm: ", err)
	}
	defer h.close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	server := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Synthetic GitHub beta harness listening on %s", h.cfg.PublicOrigin)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("harness server failed")
	}
}

func (h *harness) open() error {
	database, err := db.Open(context.Background(), h.cfg.DB)
	if err != nil {
		return err
	}
	h.database = database
	data := store.New(database)
	actor, err := data.EnsureDisabledActor(context.Background())
	if err != nil {
		return err
	}
	if _, err = data.GetProject(context.Background(), "GHBETA"); err != nil {
		key, name, title := "GHBETA", "Synthetic populated beta", "Existing task retained across restart"
		project, err := data.CreateProject(context.Background(), store.ProjectInput{Key: &key, Name: &name}, actor.ID)
		if err != nil {
			return err
		}
		if _, err = data.CreateTask(context.Background(), project.ID, store.TaskInput{Title: &title}, actor.ID); err != nil {
			return err
		}
	}
	provider := githubprovider.New(githubprovider.Options{Enabled: true, TokenFile: h.cfg.GitHubTokenFile,
		Repositories: h.cfg.GitHubRepositories, Endpoint: h.upstream.URL, HTTPClient: h.upstream.Client()})
	s := httpapi.New(data, auth.NewManager(data, h.cfg), h.cfg, h.ai)
	s.Github = httpapi.NewGithubService(data, provider)
	s.GithubAudits = httpapi.NewGithubAuditService(data, provider, h.ai, nil)
	s.StartGithubServices(context.Background())
	h.server = s
	return nil
}

func (h *harness) close() {
	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = h.server.StopGithubServices(ctx)
	}
	if h.database != nil {
		_ = h.database.Close()
	}
}

func (h *harness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/__githubbeta/restart" {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if !h.controlOrigin(r) {
			w.WriteHeader(403)
			return
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.close()
		if err := h.open(); err != nil {
			http.Error(w, "restart failed", 500)
			return
		}
		writeJSON(w, map[string]bool{"restarted": true})
		return
	}
	if r.URL.Path == "/__githubbeta/state" {
		if r.Method == http.MethodPut {
			if !h.controlOrigin(r) {
				w.WriteHeader(403)
				return
			}
			var input struct {
				Revoked *bool `json:"revoked"`
				DelayMS *int  `json:"delay_ms"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil {
				http.Error(w, "invalid state", 400)
				return
			}
			h.state.mu.Lock()
			if input.Revoked != nil {
				h.state.revoked = *input.Revoked
			}
			if input.DelayMS != nil && *input.DelayMS >= 0 && *input.DelayMS <= 30000 {
				h.state.delayMS = *input.DelayMS
			}
			h.state.mu.Unlock()
		} else if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		h.state.mu.Lock()
		defer h.state.mu.Unlock()
		writeJSON(w, map[string]any{"revoked": h.state.revoked, "delay_ms": h.state.delayMS, "upstream_requests": h.state.requests, "ai_calls": h.state.aiCalls})
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	h.server.ServeHTTP(w, r)
}

func (h *harness) controlOrigin(r *http.Request) bool {
	// Command-line requests have no Origin; browser cross-origin writes are denied.
	return r.Header.Get("Origin") == "" || r.Header.Get("Origin") == h.cfg.PublicOrigin
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, "synthetic response write failed")
	}
}
