package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/store"
)

// These scenarios are specified before the token guard implementation:
// sources/disabled agents must reject new credentials without changing tokens
// or events; ID/key/slug grants outside the ceiling must fail; empty grants
// inherit; legacy tokens retain effective intersections and explicit revocation.
// Source-looking labels cannot create server-owned provenance.
// These are HTTP/SQLite integration regressions for the failure scenarios in
// docs/AGENT_TOKEN_SERVER_FAILURES.md. Never print issuance bodies or bearers.
func tokenAccessProject(t *testing.T, server *Server, key string) store.Project {
	t.Helper()
	r := request(t, server, http.MethodPost, "/api/v1/projects", map[string]any{
		"key": key, "name": key,
	}, map[string]string{"Content-Type": "application/json"})
	if r.Code != http.StatusCreated {
		t.Fatalf("project creation returned %d", r.Code)
	}
	var p store.Project
	if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func tokenAccessIssue(t *testing.T, server *Server, actor string, projects []string) int {
	t.Helper()
	if projects == nil {
		projects = []string{}
	}
	r := request(t, server, http.MethodPost, "/api/v1/agents/"+actor+"/tokens", map[string]any{
		"name": "Synthetic access probe", "scopes": []string{"projects:read", "tasks:read"}, "project_ids": projects,
	}, map[string]string{"Content-Type": "application/json"})
	tokenAccessArtifact(t, map[string]any{"operation": "issue", "status": r.Code})
	return r.Code
}

// Artifacts deliberately contain only synthetic operation names and status
// codes. Issuance response bodies and bearer values must never enter artifacts.
func tokenAccessArtifact(t *testing.T, proof map[string]any) {
	t.Helper()
	dir := os.Getenv("HELM_TEST_ARTIFACT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, t.Name()+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(proof); err != nil {
		t.Fatal(err)
	}
	t.Logf("sanitized HTTP proof: %s", path)
}

func tokenAccessCreatedEvents(t *testing.T, data *store.Store) int {
	t.Helper()
	var count int
	if err := data.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM events WHERE type='token.created'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAgentTokenRejectsProjectsOutsideCeiling(t *testing.T) {
	s, data := testServer(t, "disabled")
	ctx := context.Background()
	a := tokenAccessProject(t, s, "ALLOW")
	b := tokenAccessProject(t, s, "DENY")
	agent, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Limited worker", ProjectIDs: []string{a.ID}}, "actor-disabled-mode", "")
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore := tokenAccessCreatedEvents(t, data)
	for _, ids := range [][]string{{b.ID}, {a.ID, b.Key}, {b.Slug}} {
		if status := tokenAccessIssue(t, s, agent.ID, ids); status != http.StatusBadRequest {
			t.Errorf("outside-ceiling issuance status %d, want 400", status)
		}
	}
	tokens, err := data.ListTokens(ctx, agent.ID)
	if err != nil || len(tokens) != 0 {
		t.Fatalf("rejected issuance changed token inventory: count=%d error=%v", len(tokens), err)
	}
	if after := tokenAccessCreatedEvents(t, data); after != eventsBefore {
		t.Error("rejected issuance created token events")
	}
	for _, ids := range [][]string{{a.ID}, {a.Key}, {a.Slug}, {}} {
		if status := tokenAccessIssue(t, s, agent.ID, ids); status != http.StatusCreated {
			t.Errorf("allowed/inherited issuance status %d, want 201", status)
		}
	}
	// Legacy stored restrictions continue to intersect with the actor ceiling.
	_, raw, err := data.CreateToken(ctx, agent.ID, "Legacy mixed grant", []string{"projects:read"}, []string{a.ID, b.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + raw}
	if status := request(t, s, http.MethodGet, "/api/v1/projects/"+a.ID, nil, headers).Code; status != http.StatusOK {
		t.Errorf("legacy allowed read status %d", status)
	}
	if status := request(t, s, http.MethodGet, "/api/v1/projects/"+b.ID, nil, headers).Code; status < 400 {
		t.Errorf("legacy outside-ceiling read status %d, want denial", status)
	}
}

func TestAgentTokenSourceClassificationPreservesLegacyCredentials(t *testing.T) {
	s, data := testServer(t, "disabled")
	ctx := context.Background()
	p := tokenAccessProject(t, s, "SOURCE")
	_, err := data.IngestAlerts(ctx, store.AlertIntakeRoute{
		Integration: "webhook-synthetic", ActorName: "Synthetic source", ProjectRef: p.ID,
	}, []store.IntakeAlert{{AlertType: "synthetic", FamilyKey: "family", ConditionKey: "condition", ResourceID: "resource", ResourceName: "Synthetic service", Title: "Synthetic alert", Priority: "normal"}})
	if err != nil {
		t.Fatal(err)
	}
	const sourceID = "actor-webhook-synthetic-intake"
	legacy, raw, err := data.CreateToken(ctx, sourceID, "Legacy source credential", []string{"projects:read"}, []string{p.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Description and display-name changes cannot change server provenance.
	if _, err := data.DB.ExecContext(ctx, `UPDATE actors SET name='Renamed source', description='ordinary worker' WHERE id=?`, sourceID); err != nil {
		t.Fatal(err)
	}
	ordinary, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Synthetic source", Description: "Records external alerts. Holds no credentials and cannot sign in."}, "actor-disabled-mode", "")
	if err != nil {
		t.Fatal(err)
	}
	r := request(t, s, http.MethodGet, "/api/v1/agents", nil, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("agent inventory status %d", r.Code)
	}
	var inventory struct {
		Data []struct {
			ID           string `json:"id"`
			IntakeSource bool   `json:"intake_source"`
			Tokens       []struct {
				ID    string `json:"id"`
				Token string `json:"token"`
			} `json:"tokens"`
		} `json:"data"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	sourceFound := false
	for _, actor := range inventory.Data {
		if actor.ID == sourceID {
			sourceFound = true
			if !actor.IntakeSource || len(actor.Tokens) != 1 || actor.Tokens[0].ID != legacy.ID || actor.Tokens[0].Token != "" {
				t.Error("source inventory must classify provenance and retain only legacy token metadata")
			}
		}
		if actor.ID == ordinary.ID && actor.IntakeSource {
			t.Error("editable labels incorrectly classified an automation agent as a source")
		}
	}
	if !sourceFound {
		t.Fatal("source missing from inventory")
	}
	eventsBefore := tokenAccessCreatedEvents(t, data)
	if status := tokenAccessIssue(t, s, sourceID, []string{p.ID}); status != http.StatusBadRequest {
		t.Errorf("source issuance status %d, want 400", status)
	}
	if after := tokenAccessCreatedEvents(t, data); after != eventsBefore {
		t.Error("source rejection created token events")
	}
	tokens, err := data.ListTokens(ctx, sourceID)
	if err != nil || len(tokens) != 1 || tokens[0].ID != legacy.ID {
		t.Error("source issuance rejection changed legacy token inventory")
	}
	if status := tokenAccessIssue(t, s, ordinary.ID, nil); status != http.StatusCreated {
		t.Errorf("ordinary agent issuance status %d", status)
	}
	headers := map[string]string{"Authorization": "Bearer " + raw}
	if status := request(t, s, http.MethodGet, "/api/v1/projects/"+p.ID, nil, headers).Code; status != http.StatusOK {
		t.Errorf("legacy source token was silently disabled: status %d", status)
	}
	if status := request(t, s, http.MethodDelete, "/api/v1/tokens/"+legacy.ID, nil, nil).Code; status != http.StatusNoContent {
		t.Errorf("explicit legacy revocation status %d", status)
	}
	if status := request(t, s, http.MethodGet, "/api/v1/projects/"+p.ID, nil, headers).Code; status != http.StatusUnauthorized {
		t.Errorf("revoked legacy credential status %d, want 401", status)
	}
}

func TestAgentTokenRejectsDisabledIdentity(t *testing.T) {
	s, data := testServer(t, "disabled")
	ctx := context.Background()
	tokenAccessProject(t, s, "DISABLED")
	agent, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Disabled worker"}, "actor-disabled-mode", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.DB.ExecContext(ctx, `UPDATE actors SET disabled_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), agent.ID); err != nil {
		t.Fatal(err)
	}
	before := tokenAccessCreatedEvents(t, data)
	if status := tokenAccessIssue(t, s, agent.ID, nil); status != http.StatusBadRequest {
		t.Errorf("disabled-agent issuance status %d, want 400", status)
	}
	tokens, err := data.ListTokens(ctx, agent.ID)
	if err != nil || len(tokens) != 0 {
		t.Error("disabled issuance changed token inventory")
	}
	if after := tokenAccessCreatedEvents(t, data); after != before {
		t.Error("disabled issuance created token events")
	}
}

func TestAgentTokenUnrestrictedAndPublicNamespace(t *testing.T) {
	s, _ := testServer(t, "disabled")
	p := tokenAccessProject(t, s, "PUBLIC")
	// A copied source label stays an ordinary random-ID agent; client-owned
	// provenance fields are rejected by the public creation contract.
	r := request(t, s, http.MethodPost, "/api/v1/agents", map[string]any{
		"name": "Webhook intake", "description": "Records external alerts.",
	}, map[string]string{"Content-Type": "application/json"})
	if r.Code != http.StatusCreated {
		t.Fatalf("public agent creation status %d", r.Code)
	}
	var agent struct {
		ID           string `json:"id"`
		IntakeSource bool   `json:"intake_source"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &agent); err != nil {
		t.Fatal(err)
	}
	if agent.IntakeSource || strings.HasPrefix(agent.ID, "actor-") {
		t.Fatal("public creation used server-owned namespace")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(r.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["intake_source"]; present {
		t.Error("ordinary agent must omit intake_source metadata")
	}
	for _, ids := range [][]string{nil, {p.ID}} {
		if status := tokenAccessIssue(t, s, agent.ID, ids); status != http.StatusCreated {
			t.Errorf("unrestricted issuance status %d", status)
		}
	}
	for _, field := range []string{"id", "intake_source"} {
		forged := request(t, s, http.MethodPost, "/api/v1/agents", map[string]any{"name": "Forged source", field: "actor-forged-intake"}, map[string]string{"Content-Type": "application/json"})
		if forged.Code != http.StatusBadRequest {
			t.Errorf("client provenance field %s status %d", field, forged.Code)
		}
		tokenAccessArtifact(t, map[string]any{"operation": "reject-client-" + field, "status": forged.Code})
	}
}

func TestAgentTokenInheritedCeilingRemainsEffectiveHTTP(t *testing.T) {
	s, data := testServer(t, "disabled")
	ctx := context.Background()
	allowed := tokenAccessProject(t, s, "INHERIT")
	denied := tokenAccessProject(t, s, "OUTSIDE")
	agent, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Inherited ceiling", ProjectIDs: []string{allowed.ID}}, "actor-disabled-mode", "")
	if err != nil {
		t.Fatal(err)
	}
	r := request(t, s, http.MethodPost, "/api/v1/agents/"+agent.ID+"/tokens", map[string]any{"name": "Inherited", "scopes": []string{"projects:read"}, "project_ids": []string{}}, map[string]string{"Content-Type": "application/json"})
	if r.Code != http.StatusCreated {
		t.Fatalf("inherit issue status %d", r.Code)
	}
	var issued struct {
		Token      string   `json:"token"`
		ProjectIDs []string `json:"project_ids"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || len(issued.ProjectIDs) != 0 {
		t.Fatal("inheritance changed stored restriction")
	}
	headers := map[string]string{"Authorization": "Bearer " + issued.Token}
	for _, probe := range []struct {
		project string
		status  int
	}{{allowed.ID, http.StatusOK}, {denied.ID, http.StatusForbidden}} {
		got := request(t, s, http.MethodGet, "/api/v1/projects/"+probe.project, nil, headers).Code
		if got != probe.status {
			t.Errorf("inherited effective access status %d want %d", got, probe.status)
		}
		tokenAccessArtifact(t, map[string]any{"operation": "inheritance-read", "status": got, "expected": probe.status})
	}
	// Bearer callers cannot administer credentials; native human membership
	// authority remains the issuance gate.
	got := request(t, s, http.MethodPost, "/api/v1/agents/"+agent.ID+"/tokens", map[string]any{"name": "Unauthorized", "scopes": []string{"projects:read"}}, headers).Code
	if got != http.StatusForbidden {
		t.Errorf("bearer token issuance status %d", got)
	}
	tokenAccessArtifact(t, map[string]any{"operation": "reject-bearer-admin", "status": got})
}
