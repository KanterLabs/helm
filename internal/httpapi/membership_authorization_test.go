package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func TestMembershipPendingConfiguredOwnerAndTokenIntersection(t *testing.T) {
	server, data := testServer(t, "cloudflare")
	ctx := context.Background()
	// A verified stranger before the owner must not consume bootstrap eligibility.
	pending, err := data.EnsureCloudflareActor(ctx, "pending@example.test", "Pending", "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	server.Auth.CloudflareVerifier = staticCloudflareVerifier{claims: auth.CloudflareClaims{Email: "pending@example.test"}}
	headers := map[string]string{"Cf-Access-Jwt-Assertion": "synthetic-assertion"}
	for _, path := range []string{"/projects", "/intake", "/sidebar-counts", "/views", "/codex/runs", "/notifications", "/export"} {
		res := request(t, server, "GET", "/api/v1"+path, nil, headers)
		if res.Code != 403 {
			t.Fatalf("pending %s=%d %s", path, res.Code, res.Body)
		}
	}
	owner, err := data.EnsureCloudflareActor(ctx, "owner@example.test", "Owner", "owner@example.test")
	if err != nil || !owner.Admin {
		t.Fatalf("bootstrap after pending: %v %+v", err, owner)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("GRANT"), Name: stringPtr("Granted")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("HIDDEN"), Name: stringPtr("Hidden")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := data.UpdateMember(ctx, pending.ID, store.MemberUpdate{ProjectIDs: &[]string{project.ID}}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !admitted.Admin && len(admitted.ProjectIDs) != 1 {
		t.Fatal("admission grants missing")
	}
	_, raw, err := data.CreateToken(ctx, pending.ID, "Synthetic", []string{"projects:read", "tasks:read", "events:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokenHeaders := map[string]string{"Authorization": "Bearer " + raw}
	if res := request(t, server, "GET", "/api/v1/projects/"+hidden.ID, nil, tokenHeaders); res.Code != 403 {
		t.Fatalf("token widened membership: %d", res.Code)
	}
	_, err = data.UpdateMember(ctx, pending.ID, store.MemberUpdate{ProjectIDs: &[]string{}}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := data.GetActor(ctx, pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := scopedProjectIDs(auth.Identity{Actor: actor})
	if ids == nil || len(ids) != 0 {
		t.Fatalf("empty ceiling=%#v", ids)
	}
	for _, path := range []string{"/projects", "/search?q=Hidden", "/intake", "/sidebar-counts", "/my-work", "/export", "/notifications", "/views"} {
		res := request(t, server, "GET", "/api/v1"+path, nil, headers)
		if res.Code != 200 {
			t.Fatalf("empty member %s=%d %s", path, res.Code, res.Body)
		}
		if strings.Contains(res.Body.String(), hidden.ID) || strings.Contains(res.Body.String(), project.ID) {
			t.Fatalf("empty member leaked %s", path)
		}
	}
	// Reviewed demotion cannot be reversed by configured-email reconciliation.
	_, err = data.UpdateMember(ctx, pending.ID, store.MemberUpdate{Role: stringPtr("admin")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = data.UpdateMember(ctx, owner.ID, store.MemberUpdate{Role: stringPtr("member")}, pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := data.EnsureCloudflareActor(ctx, "owner@example.test", "Owner", "owner@example.test")
	if err != nil || refreshed.Admin {
		t.Fatalf("silent reelevation: %+v %v", refreshed, err)
	}
	_, err = data.DisableMember(ctx, pending.ID, owner.ID)
	if err == nil {
		t.Fatal("last admin disable succeeded")
	}
}

func TestMembershipGenericHTTPLoginAndReset(t *testing.T) {
	server, data := testServer(t, "local")
	ctx := context.Background()
	hash, err := auth.HashPassword("synthetic-password")
	if err != nil {
		t.Fatal(err)
	}
	email := "owner@example.test"
	owner, err := data.CreateFirstAdmin(ctx, store.Actor{Name: "Owner", Email: &email}, hash)
	if err != nil {
		t.Fatal(err)
	}
	email = "member@example.test"
	member, err := data.CreateMember(ctx, store.Actor{Name: "Member", Email: &email}, "member", nil, hash, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	var first string
	for _, email := range []string{"unknown@example.test", "owner@example.test", "member@example.test"} {
		if email == "member@example.test" {
			_, err = data.DisableMember(ctx, member.ID, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		for attempt := 0; attempt < 7; attempt++ {
			res := request(t, server, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "synthetic-wrong-password"}, nil)
			if res.Code != http.StatusUnauthorized {
				t.Fatalf("login %d=%d", attempt, res.Code)
			}
			var value any
			_ = json.Unmarshal(res.Body.Bytes(), &value)
			if first == "" {
				first = res.Body.String()
			}
			if res.Body.String() != first {
				t.Fatal("credential failures distinguish identity/rate state")
			}
		}
	}
	if err := data.CreateSession(ctx, owner.ID, "synthetic-owner-cookie", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := data.SetMemberPassword(ctx, owner.ID, hash, owner.ID); err != nil {
		t.Fatal(err)
	}
	if res := membershipRequest(t, server, "GET", "/api/v1/projects", "synthetic-owner-cookie", nil); res.Code != 401 {
		t.Fatalf("reset cookie=%d", res.Code)
	}
}

func TestMembershipRevocationHidesSavedViewsAndPrivateRuns(t *testing.T) {
	server, data := testServer(t, "local")
	ctx := context.Background()
	email := "owner@example.test"
	owner, err := data.CreateFirstAdmin(ctx, store.Actor{Name: "Owner", Email: &email}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("PRIVATE"), Name: stringPtr("Private fixture")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	email = "member@example.test"
	member, err := data.CreateMember(ctx, store.Actor{Name: "Member", Email: &email}, "member", []string{project.ID}, "fixture", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = data.CreateSession(ctx, member.ID, "synthetic-cookie", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	shared := true
	name := "Private view"
	view, err := data.CreateSavedView(ctx, member.ID, store.SavedViewInput{Name: &name, Shared: &shared, FiltersSet: true, Filters: map[string]any{"project": project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := data.StartLunaRun(ctx, store.LunaRunStart{ActorID: member.ID, ProjectID: project.ID, ProjectKey: project.Key, Feature: "task_draft", Model: "synthetic", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = data.UpdateMember(ctx, member.ID, store.MemberUpdate{ProjectIDs: &[]string{}}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/views/" + view.ID, "/codex/runs/" + run.ID} {
		res := membershipRequest(t, server, "GET", "/api/v1"+path, "synthetic-cookie", nil)
		if res.Code != 404 {
			t.Errorf("revoked resource %s=%d", path, res.Code)
		}
	}
	for _, path := range []string{"/views", "/codex/runs"} {
		res := membershipRequest(t, server, "GET", "/api/v1"+path, "synthetic-cookie", nil)
		if res.Code != 200 || strings.Contains(res.Body.String(), project.ID) {
			t.Errorf("revoked collection %s=%d %s", path, res.Code, res.Body)
		}
	}
}
