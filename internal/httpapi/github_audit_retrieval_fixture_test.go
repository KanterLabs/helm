package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/KanterLabs/helm/internal/codexruntime"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const retrievalSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const retrievalHead = "cccccccccccccccccccccccccccccccccccccccc"
const retrievalBlob = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type retrievalFixture struct {
	files   map[string]string
	hints   map[string][]string
	denied  map[string]bool
	reads   map[string]int
	queries []string
	pr      bool
}

func (f *retrievalFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/search/code":
		q := r.URL.Query().Get("q")
		f.queries = append(f.queries, q)
		paths := []map[string]any{}
		for needle, hints := range f.hints {
			if strings.Contains(q, needle) {
				for _, p := range hints {
					paths = append(paths, map[string]any{"path": p, "repository": map[string]any{"full_name": "example/synthetic"}})
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"total_count": len(paths), "incomplete_results": false, "items": paths})
	case strings.Contains(r.URL.Path, "/git/trees/"):
		sha := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		tree := []map[string]any{}
		for key, text := range f.files {
			parts := strings.SplitN(key, ":", 2)
			if parts[0] == sha {
				tree = append(tree, map[string]any{"path": parts[1], "type": "blob", "mode": "100644", "sha": retrievalBlob, "size": len(text)})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"tree": tree, "truncated": false})
	case strings.Contains(r.URL.Path, "/contents/"):
		path := strings.SplitN(r.URL.Path, "/contents/", 2)[1]
		key := r.URL.Query().Get("ref") + ":" + path
		f.reads[key]++
		if f.denied[key] {
			w.WriteHeader(404)
			return
		}
		text, ok := f.files[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "file", "encoding": "base64", "sha": retrievalBlob, "size": len(text), "content": base64.StdEncoding.EncodeToString([]byte(text))})
	case strings.Contains(r.URL.Path, "/commits/") && !strings.HasSuffix(r.URL.Path, "/statuses") && !strings.HasSuffix(r.URL.Path, "/check-runs"):
		json.NewEncoder(w).Encode(map[string]any{"sha": retrievalSHA})
	case strings.HasSuffix(r.URL.Path, "/pulls/7/files"):
		json.NewEncoder(w).Encode([]map[string]any{{"filename": "internal/opaque.go"}})
	case strings.HasSuffix(r.URL.Path, "/pulls/7"):
		json.NewEncoder(w).Encode(map[string]any{"title": "Synthetic implementation", "state": "open", "head": map[string]any{"sha": retrievalHead}, "base": map[string]any{"sha": retrievalSHA, "repo": map[string]any{"id": 1}}})
	case strings.HasSuffix(r.URL.Path, "/check-runs"):
		fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
	case strings.HasSuffix(r.URL.Path, "/statuses"), strings.HasSuffix(r.URL.Path, "/reviews"):
		fmt.Fprint(w, `[]`)
	default:
		fmt.Fprint(w, `{"id":1,"full_name":"example/synthetic","default_branch":"main","permissions":{"pull":true}}`)
	}
}

func newRetrievalProvider(t *testing.T, f *retrievalFixture) *githubprovider.Client {
	t.Helper()
	f.reads = map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("SYNTHETIC_RETRIEVAL_TOKEN"), 0600); err != nil {
		t.Fatal(err)
	}
	return githubprovider.New(githubprovider.Options{Enabled: true, TokenFile: token, Repositories: []string{"example/synthetic"}, HTTPClient: server.Client(), Endpoint: server.URL})
}

type retrievalAI struct{ calls int }

func (a *retrievalAI) Account(context.Context, string, bool) (codexruntime.AccountStatus, error) {
	return codexruntime.AccountStatus{Connected: true}, nil
}
func (a *retrievalAI) DraftEvidence(_ context.Context, _ string, r codexruntime.RunRequest) (codexruntime.RunResult, error) {
	a.calls++
	var input struct {
		Evidence store.GithubAuditEvidence `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(r.Prompt), &input); err != nil {
		return codexruntime.RunResult{}, err
	}
	findings := []map[string]any{}
	for _, task := range input.Evidence.Tasks {
		findings = append(findings, map[string]any{"task_id": task.ID, "verdict": "needs_attention", "confidence": 0.5, "reason": "Synthetic retrieval receipt only", "evidence_refs": []string{}})
	}
	raw, _ := json.Marshal(map[string]any{"findings": findings})
	return codexruntime.RunResult{Status: "completed", Output: string(raw)}, nil
}

func writeRetrievalReceipt(t *testing.T, name string, e store.GithubAuditEvidence, f *retrievalFixture) {
	t.Helper()
	raw, err := json.MarshalIndent(map[string]any{"evidence": e, "reads": f.reads, "queries": f.queries}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SYNTHETIC_RETRIEVAL_TOKEN") {
		t.Fatal("credential in receipt")
	}
	dir, err := os.MkdirTemp("", "helm-retrieval-proof-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("Sanitized capture receipt: %s", path)
}
