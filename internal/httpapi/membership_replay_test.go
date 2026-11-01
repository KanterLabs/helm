package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/store"
)

// Parent-review regressions are documented before implementation in
// TC113_BACKEND_FAILURES.md. These use a real listener and normal cookie auth.
type membershipReplayFixture struct {
	t             *testing.T
	data          *store.Store
	live          *httptest.Server
	owner, member store.Actor
	project       store.Project
	cookie        *http.Cookie
	evidence      []map[string]any
}

func newMembershipReplayFixture(t *testing.T, queue, grant bool) *membershipReplayFixture {
	t.Helper()
	server, data := testServer(t, "local")
	ctx := context.Background()
	email := "review-owner@example.test"
	owner, err := data.CreateFirstAdmin(ctx, store.Actor{Name: "Review owner", Email: &email}, "synthetic-hash")
	if err != nil {
		t.Fatal(err)
	}
	var project store.Project
	if queue {
		project, err = data.TicketQueue(ctx)
	} else {
		project, err = data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("REPLAY"), Name: stringPtr("Synthetic replay project")}, owner.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	grants := []string{}
	if grant {
		grants = append(grants, project.ID)
	}
	email = "review-member@example.test"
	member, err := data.CreateMember(ctx, store.Actor{Name: "Review member", Email: &email}, "member", grants, "synthetic-hash", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "roadmap_session", Value: "synthetic-review-cookie"}
	if err = data.CreateSession(ctx, member.ID, cookie.Value, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	f := &membershipReplayFixture{t: t, data: data, live: httptest.NewServer(server), owner: owner, member: member.Actor, project: project, cookie: cookie}
	t.Cleanup(func() {
		f.live.Close()
		if dir := os.Getenv("HELM_TEST_ARTIFACT_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Error(err)
				return
			}
			body, _ := json.MarshalIndent(f.evidence, "", "  ")
			if err := os.WriteFile(filepath.Join(dir, t.Name()+".json"), body, 0600); err != nil {
				t.Error(err)
			}
		}
	})
	return f
}

func (f *membershipReplayFixture) call(method, path, key string, input any, bearer string) (int, string) {
	f.t.Helper()
	var payload []byte
	if input != nil {
		payload, _ = json.Marshal(input)
	}
	req, err := http.NewRequest(method, f.live.URL+"/api/v1"+path, bytes.NewReader(payload))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Origin", "http://helm.test")
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if bearer == "" {
		req.AddCookie(f.cookie)
	} else {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := f.live.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	f.evidence = append(f.evidence, map[string]any{"method": method, "path": path, "status": response.StatusCode, "response": string(body)})
	return response.StatusCode, string(body)
}

func TestMembershipReplayRevokedGrantsHTTP(t *testing.T) {
	f := newMembershipReplayFixture(t, true, true)
	_, err := f.data.DB.Exec(`INSERT INTO notifications(id,recipient_id,actor_id,event_type,project_id,title,body,payload,dedupe_key,created_at,updated_at) VALUES ('review-notification',?,?,'task.updated',?,'SYNTHETIC_PRIVATE_TITLE','SYNTHETIC_PRIVATE_BODY','{}','review-notification','2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`, f.member.ID, f.owner.ID, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, path string
		input              any
		status             int
	}{
		{"notification", "PATCH", "/notifications/review-notification", map[string]any{"read": true}, 200},
		{"ticket", "POST", "/tickets", map[string]any{"title": "SYNTHETIC_PRIVATE_TICKET"}, 201},
		{"view", "POST", "/views", map[string]any{"name": "SYNTHETIC_PRIVATE_VIEW", "filters": map[string]any{"project": f.project.ID}, "shared": true}, 201},
		{"watch", "POST", "/watches", map[string]any{"project_id": f.project.ID}, 201},
	}
	for _, item := range cases {
		status, body := f.call(item.method, item.path, "review-"+item.name, item.input, "")
		if status != item.status {
			t.Fatalf("initial %s: %d %s", item.name, status, body)
		}
		retry, replay := f.call(item.method, item.path, "review-"+item.name, item.input, "")
		if retry != status || replay != body {
			t.Fatalf("unchanged retry %s: %d %s", item.name, retry, replay)
		}
	}
	if _, err := f.data.UpdateMember(context.Background(), f.member.ID, store.MemberUpdate{ProjectIDs: &[]string{}}, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	for _, item := range cases {
		status, body := f.call(item.method, item.path, "review-"+item.name, item.input, "")
		if status != 403 && status != 404 && status != 409 {
			t.Errorf("revoked %s replay disclosed cached response: %d %s", item.name, status, body)
		}
		if strings.Contains(body, "SYNTHETIC_PRIVATE") || strings.Contains(body, f.project.ID) {
			t.Errorf("revoked %s replay retained private payload", item.name)
		}
	}
}

func TestMembershipLabelDeletionRetryHTTP(t *testing.T) {
	f := newMembershipReplayFixture(t, false, true)
	label, err := f.data.CreateLabel(context.Background(), f.project.ID, store.LabelInput{Name: "Synthetic retry label"}, f.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		status, body := f.call("DELETE", "/labels/"+label.ID, "review-delete-label", nil, "")
		if status != 204 {
			t.Fatalf("deletion attempt %d: %d %s", attempt, status, body)
		}
	}
}

func TestMembershipDeniedTicketDoesNotCreateQueueHTTP(t *testing.T) {
	f := newMembershipReplayFixture(t, false, false)
	var before int
	if err := f.data.DB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, token, err := f.data.CreateToken(context.Background(), f.owner.ID, "Synthetic readonly", []string{"tasks:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, bearer := range []string{"", token} {
		status, body := f.call("POST", "/tickets", "", map[string]any{"title": "Synthetic denied ticket"}, bearer)
		if status != 403 {
			t.Fatalf("denied ticket: %d %s", status, body)
		}
		var after int
		if err := f.data.DB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Errorf("denied ticket created project: %d -> %d", before, after)
		}
		if _, found, err := f.data.LookupTicketQueue(context.Background()); err != nil || found {
			t.Errorf("denied ticket created queue: found=%v err=%v", found, err)
		}
	}
}
