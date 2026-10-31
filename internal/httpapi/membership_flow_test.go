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
)

// This journey uses an actual listener, cookie authentication and the normal
// admission/CSRF/router stack. Only synthetic redacted responses are captured.
func TestMembershipHTTPJourney(t *testing.T) {
	server, data := testServer(t, "local")
	live := httptest.NewServer(server)
	defer live.Close()
	var evidence []map[string]any
	call := func(method, path string, input any, cookie *http.Cookie, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(input)
		req, _ := http.NewRequest(method, live.URL+"/api/v1"+path, bytes.NewReader(raw))
		req.Header.Set("Origin", "http://helm.test")
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res, err := live.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		evidence = append(evidence, map[string]any{"method": method, "path": path, "status": res.StatusCode, "response": string(body)})
		if res.StatusCode != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, res.StatusCode, want, body)
		}
		var out map[string]any
		_ = json.Unmarshal(body, &out)
		return out
	}
	login := func(email, password string) *http.Cookie {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"email": email, "password": password})
		req, _ := http.NewRequest("POST", live.URL+"/api/v1/auth/login", bytes.NewReader(raw))
		req.Header.Set("Origin", "http://helm.test")
		req.Header.Set("Content-Type", "application/json")
		res, err := live.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 || len(res.Cookies()) == 0 {
			t.Fatalf("login failed: %d", res.StatusCode)
		}
		return res.Cookies()[0]
	}
	t.Cleanup(func() {
		artifact := filepath.Join(t.TempDir(), "membership-http-responses.json")
		if dir := os.Getenv("HELM_TEST_ARTIFACT_DIR"); dir != "" {
			_ = os.MkdirAll(dir, 0700)
			artifact = filepath.Join(dir, "membership-http-responses.json")
		}
		body, _ := json.MarshalIndent(evidence, "", "  ")
		if err := os.WriteFile(artifact, body, 0600); err != nil {
			t.Error(err)
		}
		t.Logf("sanitized artifact: %s", artifact)
	})
	call("POST", "/auth/setup", map[string]string{"email": "owner@example.test", "name": "Owner", "password": "synthetic-owner-password"}, nil, 201)
	owner := login("owner@example.test", "synthetic-owner-password")
	p := call("POST", "/projects", map[string]string{"key": "MEM", "name": "Membership fixture"}, owner, 201)
	projectID := p["id"].(string)
	member := call("POST", "/members", map[string]any{"email": "member@example.test", "name": "Member", "password": "synthetic-member-password", "project_ids": []string{projectID}}, owner, 201)
	memberID := member["id"].(string)
	first := login("member@example.test", "synthetic-member-password")
	second := login("member@example.test", "synthetic-member-password")
	call("GET", "/projects/"+projectID, nil, first, 200)
	task := call("POST", "/projects/"+projectID+"/tasks", map[string]any{"title": "Synthetic membership work", "assignee": memberID}, owner, 201)
	taskID := task["id"].(string)
	audit := call("POST", "/projects/"+projectID+"/audits", map[string]string{"scope": "board", "status": "queued"}, owner, 201)
	auditID := audit["id"].(string)
	hidden := call("POST", "/projects", map[string]string{"key": "SECRET", "name": "Synthetic hidden project"}, owner, 201)
	call("POST", "/projects/"+hidden["id"].(string)+"/tasks", map[string]string{"title": "Synthetic hidden work"}, owner, 201)
	summary := call("GET", "/roadmap", nil, first, 200)
	if summary["total_tasks"] != float64(1) {
		t.Fatalf("scoped Roadmap total=%v", summary["total_tasks"])
	}
	view := call("POST", "/views", map[string]any{"name": "Synthetic project view", "filters": map[string]string{"project": projectID}, "shared": true}, first, 201)
	viewID := view["id"].(string)
	call("GET", "/audits/"+auditID, nil, first, 200)
	call("PATCH", "/members/"+memberID, map[string]any{"project_ids": []string{}}, owner, 200)
	call("GET", "/projects/"+projectID, nil, first, 403)
	for _, path := range []string{"/tasks/" + taskID, "/tasks/" + taskID + "/timeline", "/audits/" + auditID, "/audits/" + auditID + "/findings", "/audits/" + auditID + "/evidence"} {
		call("GET", path, nil, first, 403)
	}
	call("GET", "/views/"+viewID, nil, first, 404)
	for _, path := range []string{"/projects", "/search?q=Synthetic", "/intake", "/sidebar-counts", "/my-work", "/notifications", "/views", "/export", "/events", "/roadmap", "/tickets"} {
		out := call("GET", path, nil, first, 200)
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), projectID) || strings.Contains(string(raw), "Membership fixture") {
			t.Fatalf("ungranted data in %s: %s", path, raw)
		}
	}
	call("GET", "/members", nil, first, 403)
	call("POST", "/auth/password", map[string]string{"current_password": "synthetic-member-password", "password": "synthetic-new-password"}, first, 200)
	call("GET", "/projects", nil, second, 401)
	call("GET", "/auth/sessions", nil, first, 200)
	call("PATCH", "/members/"+memberID, map[string]any{"disabled": true}, owner, 200)
	call("GET", "/projects", nil, first, 401)
	call("PATCH", "/members/"+memberID, map[string]any{"disabled": false}, owner, 200)
	call("GET", "/projects", nil, first, 401)
	third := login("member@example.test", "synthetic-new-password")
	sessionList := call("GET", "/auth/sessions", nil, third, 200)
	for _, item := range sessionList["data"].([]any) {
		session := item.(map[string]any)
		if _, revoked := session["revoked_at"]; !revoked {
			call("DELETE", "/auth/sessions/"+session["id"].(string), nil, third, 204)
			break
		}
	}
	call("GET", "/projects", nil, third, 401)
	fourth := login("member@example.test", "synthetic-new-password")
	call("POST", "/members/"+memberID+"/password", map[string]string{"password": "synthetic-reset-password"}, owner, 200)
	call("GET", "/projects", nil, fourth, 401)
	fifth := login("member@example.test", "synthetic-reset-password")
	call("POST", "/auth/logout", nil, fifth, 200)
	call("GET", "/projects", nil, fifth, 401)
	var payloads string
	rows, err := data.DB.QueryContext(context.Background(), `SELECT payload FROM events`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v string
		_ = rows.Scan(&v)
		payloads += v
	}
	rows.Close()
	for _, secret := range []string{"synthetic-member-password", "synthetic-new-password", first.Value, second.Value, third.Value, fourth.Value, fifth.Value, "synthetic-reset-password", "token_hash", "password_hash"} {
		if strings.Contains(payloads, secret) {
			t.Fatal("event contains secret")
		}
	}
}
