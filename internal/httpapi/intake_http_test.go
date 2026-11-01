package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

type intakeProof struct {
	Data   []store.Task `json:"data"`
	Next   string       `json:"next_cursor"`
	Counts struct {
		All    int `json:"all"`
		Triage int `json:"needs_triage"`
	} `json:"counts"`
	Queue *struct {
		ProjectID string `json:"project_id"`
	} `json:"queue"`
}

type intakeJourney struct {
	t         *testing.T
	base      string
	responses map[string]json.RawMessage
}

func newIntakeJourney(t *testing.T) (*intakeJourney, *store.Store) {
	t.Helper()
	server, data := testServer(t, "disabled")
	live := httptest.NewServer(server)
	t.Cleanup(live.Close)
	j := &intakeJourney{t: t, base: live.URL, responses: map[string]json.RawMessage{}}
	t.Cleanup(func() {
		dir := os.Getenv("HELM_TEST_ARTIFACT_DIR")
		if dir == "" {
			dir = t.TempDir()
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		body, err := json.MarshalIndent(j.responses, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, t.Name()+".json")
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("synthetic HTTP response artifact: %s", path)
	})
	return j, data
}

func (j *intakeJourney) call(name, method, path string, payload any, token string, status int) []byte {
	j.t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			j.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, j.base+path, bytes.NewReader(body))
	if err != nil {
		j.t.Fatal(err)
	}
	req.Header.Set("Origin", "http://helm.test")
	req.Header.Set("Content-Type", "application/json")
	if method == "PATCH" || strings.HasSuffix(path, "/complete") {
		resource := strings.TrimSuffix(path, "/complete")
		var current store.Task
		if err := json.Unmarshal(j.call(name+"-before", "GET", resource, nil, token, 200), &current); err != nil {
			j.t.Fatal(err)
		}
		req.Header.Set("If-Match", fmt.Sprintf(`"v%d"`, current.Version))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		j.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		j.t.Fatal(err)
	}
	if json.Valid(raw) {
		j.responses[name] = append([]byte(nil), raw...)
	}
	if res.StatusCode != status {
		j.t.Fatalf("%s: HTTP %d want %d: %s", name, res.StatusCode, status, raw)
	}
	return raw
}

func (j *intakeJourney) list(name, query, token string) intakeProof {
	j.t.Helper()
	var result intakeProof
	raw := j.call(name, http.MethodGet, "/api/v1/intake"+query, nil, token, 200)
	if err := json.Unmarshal(raw, &result); err != nil {
		j.t.Fatal(err)
	}
	if result.Data == nil {
		j.t.Fatalf("%s: data must be an array: %s", name, raw)
	}
	return result
}

func TestIntakeIdentityFilingAndPaginationHTTP(t *testing.T) {
	j, data := newIntakeJourney(t)
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("INT"), Name: stringPtr("Synthetic Intake")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	empty := j.list("empty", "?scope=all", "")
	if empty.Counts.All != 0 || empty.Next != "" || empty.Queue != nil {
		t.Fatalf("empty: %+v", empty)
	}
	if _, found, err := data.LookupTicketQueue(ctx); err != nil || found {
		t.Fatalf("read created queue: %v %v", found, err)
	}
	var ticket store.Task
	if err := json.Unmarshal(j.call("ticket", "POST", "/api/v1/tickets", map[string]any{"title": "Synthetic incoming request"}, "", 201), &ticket); err != nil {
		t.Fatal(err)
	}
	oldKey := ticket.Key
	j.call("comment", "POST", "/api/v1/tasks/"+ticket.ID+"/comments", map[string]any{"body": "Synthetic preservation evidence"}, "", 201)
	var overlap store.Task
	if err := json.Unmarshal(j.call("classify", "PATCH", "/api/v1/tasks/"+ticket.ID, map[string]any{"kind": "bug", "bug": map[string]any{"actual_behavior": "Synthetic failure", "environment": "fixture"}}, "", 200), &overlap); err != nil {
		t.Fatal(err)
	}
	j.call("file", "POST", "/api/v1/tickets/"+ticket.ID+"/file", map[string]any{"project": p.ID}, "", 200)
	var ordinary store.Task
	if err := json.Unmarshal(j.call("ordinary", "POST", "/api/v1/projects/INT/tickets", map[string]any{"title": "Synthetic ordinary request"}, "", 201), &ordinary); err != nil {
		t.Fatal(err)
	}
	var triaged store.Task
	if err := json.Unmarshal(j.call("triaged", "POST", "/api/v1/projects/INT/tasks", map[string]any{"title": "Synthetic known issue", "kind": "bug", "bug": map[string]any{"actual_behavior": "Synthetic known failure", "severity": "s2"}}, "", 201), &triaged); err != nil {
		t.Fatal(err)
	}
	j.call("non-intake", "POST", "/api/v1/projects/INT/tasks", map[string]any{"title": "Synthetic board work"}, "", 201)
	// Capture every persisted domain table that an Intake read might touch.
	before := intakeDomainSnapshot(t, data)
	all := j.list("all", "?scope=all", "")
	if all.Counts.All != 3 || all.Counts.Triage != 2 || len(all.Data) != 3 {
		t.Fatalf("all: %+v", all)
	}
	triage := j.list("triage", "", "")
	if len(triage.Data) != 2 || triage.Counts != all.Counts {
		t.Fatalf("triage: %+v", triage)
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 4; page++ {
		query := "?scope=all&limit=1"
		if cursor != "" {
			query += "&cursor=" + url.QueryEscape(cursor)
		}
		part := j.list(fmt.Sprintf("page-%d", page), query, "")
		if part.Counts != all.Counts {
			t.Fatalf("page counts changed: %+v", part)
		}
		for _, task := range part.Data {
			if seen[task.ID] {
				t.Fatalf("duplicate %s", task.ID)
			}
			seen[task.ID] = true
		}
		cursor = part.Next
		if cursor == "" {
			break
		}
	}
	if len(seen) != 3 || cursor != "" {
		t.Fatalf("pagination: %v cursor=%q", seen, cursor)
	}
	alias := j.list("old-key", "?scope=all&q="+url.QueryEscape(oldKey), "")
	if len(alias.Data) != 1 || alias.Data[0].ID != ticket.ID || alias.Data[0].ProjectID != p.ID || alias.Data[0].Bug == nil || alias.Data[0].Ticket == nil || alias.Data[0].CommentCount != 1 {
		t.Fatalf("lost filed identity: %+v", alias)
	}
	for _, tc := range []struct {
		name, query string
		all, triage int
	}{
		{"kind-task", "?scope=all&kind=task", 1, 1},
		{"kind-bug", "?scope=all&kind=bug", 2, 1},
		{"severity", "?scope=all&severity=s2", 1, 0},
		{"untriaged", "?scope=all&severity=untriaged", 1, 1},
		{"incompatible", "?scope=all&kind=task&severity=s2", 0, 0},
		{"no-match", "?scope=all&q=unmatched-synthetic", 0, 0},
	} {
		r := j.list(tc.name, tc.query, "")
		if len(r.Data) != tc.all || r.Counts.All != tc.all || r.Counts.Triage != tc.triage {
			t.Fatalf("%s: %+v", tc.name, r)
		}
	}
	for _, query := range []string{"scope=bad", "kind=request", "severity=critical", "limit=0", "cursor=invalid!", "project=INT&project=INT"} {
		j.call("invalid-"+query, "GET", "/api/v1/intake?"+query, nil, "", 400)
	}
	j.call("read-only", "POST", "/api/v1/intake", map[string]any{}, "", 405)
	if after := intakeDomainSnapshot(t, data); !reflect.DeepEqual(before, after) {
		t.Fatalf("reads changed domain data\nbefore %v\nafter %v", before, after)
	}
	resolved := j.call("resolve-old-key", "GET", "/api/v1/tasks/"+oldKey, nil, "", 200)
	var fromKey store.Task
	if err := json.Unmarshal(resolved, &fromKey); err != nil || fromKey.ID != ticket.ID {
		t.Fatalf("old key lost: %s %v", resolved, err)
	}
}

func intakeDomainSnapshot(t *testing.T, data *store.Store) map[string][]string {
	t.Helper()
	result := map[string][]string{}
	for _, table := range []string{"projects", "tasks", "tickets", "bug_details", "task_number_aliases", "comments", "events", "alert_conditions"} {
		rows, err := data.DB.Query("SELECT * FROM " + table + " ORDER BY 1")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			refs := make([]any, len(columns))
			for i := range values {
				refs[i] = &values[i]
			}
			if err := rows.Scan(refs...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			result[table] = append(result[table], fmt.Sprint(values))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	return result
}
