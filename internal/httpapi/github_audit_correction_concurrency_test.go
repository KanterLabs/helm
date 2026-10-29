package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func TestAuditCorrectionHTTPConcurrentHumans(t *testing.T) {
	f := newCorrectionFixture(t, "complete")
	other, err := f.data.CreateActor(context.Background(), store.Actor{Kind: "human", Name: "Second synthetic reviewer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// The existing test-only synthetic human fixture supplies two authenticated
	// identities. No production session/proxy authorization behavior is changed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := auth.Identity{Actor: f.actor}
		if r.URL.Path == "/reviewer" {
			identity.Actor = other
		}
		f.s.githubAuditJob(w, r, identity, f.job.AuditID, "apply")
	}))
	defer server.Close()
	f.server = server
	tokens := []string{}
	for _, path := range []string{"/owner", "/reviewer"} {
		code, raw := f.post(t, path, f.request(true, "", nil), "")
		r := correctionRow(t, code, raw)
		token, _ := r["confirmation_token"].(string)
		if token == "" {
			t.Fatalf("preview %s", raw)
		}
		tokens = append(tokens, token)
	}
	results := make(chan string, 2)
	var wg sync.WaitGroup
	for i, path := range []string{"/owner", "/reviewer"} {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			code, raw := f.post(t, path, f.request(false, tokens[i], nil), "human-once")
			r := correctionRow(t, code, raw)
			results <- r["outcome"].(string)
		}(i, path)
	}
	wg.Wait()
	close(results)
	counts := map[string]int{}
	for r := range results {
		counts[r]++
	}
	if counts["applied"] != 1 || counts["already_applied"] != 1 {
		t.Fatal(counts)
	}
	task, _ := f.data.GetTask(context.Background(), f.task.ID)
	if task.Version != f.task.Version+1 {
		t.Fatal("multiple human transitions")
	}
	correctionArtifact(t, "concurrent-humans", counts)
}
