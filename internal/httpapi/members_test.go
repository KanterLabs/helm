package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/store"
)

func TestMembershipHTTPLifecycleAndHumanCeilings(t *testing.T) {
	server, data := testServer(t, "local")
	ctx := context.Background()
	email := "owner@example.test"
	owner, err := data.CreateFirstAdmin(ctx, store.Actor{Kind: "human", Name: "Owner", Email: &email}, "owner-hash")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("OPS"), Name: stringPtr("Operations")}, owner.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	ownerCookie := "owner-cookie"
	if err := data.CreateSession(ctx, owner.ID, ownerCookie, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create owner session: %v", err)
	}

	createdResponse := membershipRequest(t, server, http.MethodPost, "/api/v1/members", ownerCookie, map[string]any{
		"email":       "member@example.test",
		"name":        "Member",
		"role":        "member",
		"project_ids": []string{project.ID},
		"password":    "member-password",
	})
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create member status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var member store.Member
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &member); err != nil {
		t.Fatalf("decode member: %v", err)
	}
	if member.WorkspaceRole != store.WorkspaceRoleMember || len(member.ProjectIDs) != 1 || member.ProjectIDs[0] != project.ID {
		t.Fatalf("created member = %#v", member)
	}
	if bytes.Contains(createdResponse.Body.Bytes(), []byte("password_hash")) {
		t.Fatal("member response exposed a password hash field")
	}

	listResponse := membershipRequest(t, server, http.MethodGet, "/api/v1/members", ownerCookie, nil)
	if listResponse.Code != http.StatusOK || !bytes.Contains(listResponse.Body.Bytes(), []byte(`"workspace_role":"member"`)) {
		t.Fatalf("list members status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}

	memberCookie := "member-cookie"
	if err := data.CreateSession(ctx, member.ID, memberCookie, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create member session: %v", err)
	}
	projectsResponse := membershipRequest(t, server, http.MethodGet, "/api/v1/projects", memberCookie, nil)
	if projectsResponse.Code != http.StatusOK || !bytes.Contains(projectsResponse.Body.Bytes(), []byte(`"id":"`+project.ID+`"`)) {
		t.Fatalf("member project ceiling status=%d body=%s", projectsResponse.Code, projectsResponse.Body.String())
	}
	if forbidden := membershipRequest(t, server, http.MethodGet, "/api/v1/members", memberCookie, nil); forbidden.Code != http.StatusForbidden {
		t.Fatalf("member directory access status = %d body=%s", forbidden.Code, forbidden.Body.String())
	}
	if forbidden := membershipRequest(t, server, http.MethodPost, "/api/v1/projects", memberCookie, map[string]any{"key": "NO", "name": "Not allowed"}); forbidden.Code != http.StatusForbidden {
		t.Fatalf("member project creation status = %d body=%s", forbidden.Code, forbidden.Body.String())
	}

	passwordResponse := membershipRequest(t, server, http.MethodPost, "/api/v1/auth/password", memberCookie, map[string]any{
		"current_password": "member-password",
		"password":         "member-password-new",
	})
	if passwordResponse.Code != http.StatusOK {
		t.Fatalf("self password change status=%d body=%s", passwordResponse.Code, passwordResponse.Body.String())
	}
	if _, _, err := data.SessionForToken(ctx, memberCookie); err != nil {
		t.Fatalf("current session was revoked by self password change: %v", err)
	}

	disabledResponse := membershipRequest(t, server, http.MethodPatch, "/api/v1/members/"+member.ID, ownerCookie, map[string]any{"disabled": true})
	if disabledResponse.Code != http.StatusOK {
		t.Fatalf("disable member status=%d body=%s", disabledResponse.Code, disabledResponse.Body.String())
	}
	if blocked := membershipRequest(t, server, http.MethodGet, "/api/v1/projects", memberCookie, nil); blocked.Code != http.StatusUnauthorized {
		t.Fatalf("disabled member request status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	if reactivated := membershipRequest(t, server, http.MethodPatch, "/api/v1/members/"+member.ID, ownerCookie, map[string]any{"disabled": false}); reactivated.Code != http.StatusOK {
		t.Fatalf("reactivate member status=%d body=%s", reactivated.Code, reactivated.Body.String())
	}
	if sessions := membershipRequest(t, server, http.MethodGet, "/api/v1/sessions?actor_id="+member.ID, ownerCookie, nil); sessions.Code != http.StatusOK {
		t.Fatalf("admin session list status=%d body=%s", sessions.Code, sessions.Body.String())
	}
	agent, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "build agent"}, owner.ID, "")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := data.CreateSession(ctx, agent.ID, "agent-session", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create agent session: %v", err)
	}
	if sessions := membershipRequest(t, server, http.MethodGet, "/api/v1/sessions?actor_id="+agent.ID, ownerCookie, nil); sessions.Code != http.StatusNotFound {
		t.Fatalf("agent session list status=%d body=%s, want 404", sessions.Code, sessions.Body.String())
	}
	if revoked := membershipRequest(t, server, http.MethodDelete, "/api/v1/sessions/does-not-exist?actor_id="+member.ID, ownerCookie, nil); revoked.Code != http.StatusNotFound {
		t.Fatalf("missing session revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
}

func membershipRequest(t *testing.T, server http.Handler, method, target, rawCookie string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = *bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, target, &body)
	if rawCookie != "" {
		request.AddCookie(&http.Cookie{Name: "roadmap_session", Value: rawCookie})
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		request.Header.Set("Origin", "http://helm.test")
	}
	result := httptest.NewRecorder()
	server.ServeHTTP(result, request)
	return result
}
