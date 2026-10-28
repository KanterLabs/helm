package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/store"
)

// The operator failure inventory was recorded in GITHUB_AGENT_AUDITS.md before
// implementation. Exercise the command against real SQLite and retain a safe
// JSON receipt in test output; no account or network service is used.
func TestAuditAccessOperatorFlow(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "operator.db")
	database, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := store.New(database)
	owner, err := s.CreateActor(ctx, store.Actor{Kind: "human", Name: "Synthetic owner", Admin: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	key, name := "OPR", "Synthetic operator"
	project, err := s.CreateProject(ctx, store.ProjectInput{Key: &key, Name: &name}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateGithubSelection(ctx, project.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1); err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Synthetic agent", ProjectIDs: []string{project.ID}}, owner.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	token, secret, err := s.CreateToken(ctx, agent.ID, "Synthetic token", []string{"tasks:read"}, []string{project.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"grant", "--db", path, "--project", key, "--owner", owner.ID, "--agent", agent.ID, "--token", token.ID}
	var out bytes.Buffer
	if err := runAuditAccess(ctx, base, &out); err != nil {
		t.Fatal(err)
	}
	var grant map[string]any
	if err := json.Unmarshal(out.Bytes(), &grant); err != nil {
		t.Fatal(err)
	}
	id, ok := grant["id"].(string)
	if !ok || id == "" || grant["max_tasks"] != float64(10) {
		t.Fatalf("invalid grant receipt: %s", out.String())
	}
	if strings.Contains(out.String(), secret) {
		t.Fatal("bearer secret leaked")
	}
	out.Reset()
	if err := runAuditAccess(ctx, []string{"list", "--db", path, "--project", key}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), id) || strings.Contains(out.String(), secret) {
		t.Fatal("invalid list receipt")
	}
	out.Reset()
	if err := runAuditAccess(ctx, []string{"revoke", "--db", path, "--authorization", id, "--owner", agent.ID}, &out); err == nil {
		t.Fatal("agent owner accepted")
	}
	if out.Len() != 0 {
		t.Fatal("failed operation emitted receipt")
	}
	if err := runAuditAccess(ctx, []string{"revoke", "--db", path, "--authorization", id, "--owner", owner.ID}, &out); err != nil {
		t.Fatal(err)
	}
	retained, err := s.GetGithubAuditAuthorization(ctx, id)
	if err != nil || retained.RevokedAt == nil {
		t.Fatalf("revocation not durable: %v", err)
	}
	t.Logf("synthetic operator receipt: %s", strings.TrimSpace(out.String()))
	for _, extra := range [][]string{{"--expires-in", "25h"}, {"--expires-in", "0s"}, {"--max-tasks", "101"}, {"--max-tasks", "0"}, {"--owner", ""}, {"extra"}} {
		out.Reset()
		args := append(append([]string{}, base...), extra...)
		if err := runAuditAccess(ctx, args, &out); err == nil {
			t.Fatalf("invalid arguments accepted: %v", extra)
		}
		if out.Len() != 0 {
			t.Fatal("invalid arguments emitted receipt")
		}
	}
}

func TestAuditAccessNewTokenFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "helm.db")
	database, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := store.New(database)
	owner, err := s.CreateActor(ctx, store.Actor{Kind: "human", Name: "Synthetic operator", Admin: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	key, name := "TFL", "Synthetic token-file"
	project, err := s.CreateProject(ctx, store.ProjectInput{Key: &key, Name: &name}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateGithubSelection(ctx, project.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1); err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Synthetic agent", ProjectIDs: []string{project.ID}}, owner.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(dir, "new-token")
	base := []string{"grant", "--db", path, "--project", project.ID, "--owner", owner.ID, "--agent", agent.ID, "--token-file", tokenPath}
	var out bytes.Buffer
	if err := runAuditAccess(ctx, base, &out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" || strings.Contains(out.String(), secret) {
		t.Fatal("missing token or secret in output")
	}
	info, err := os.Stat(tokenPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("token file is not private")
	}
	lookup, err := s.LookupToken(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !lookup.Scopes["projects:read"] || !lookup.Scopes["tasks:read"] || len(lookup.Scopes) != 2 || !lookup.Projects[project.ID] || len(lookup.Projects) != 1 {
		t.Fatal("token scope expanded")
	}
	if lookup.Token.ExpiresAt == nil {
		t.Fatal("token lacks expiry")
	}
	expiry, err := time.Parse(time.RFC3339Nano, *lookup.Token.ExpiresAt)
	if err != nil || expiry.Before(time.Now()) || expiry.After(time.Now().Add(time.Hour)) {
		t.Fatal("token expiry is not bounded")
	}
	var receipt map[string]any
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt["expires_at"] != *lookup.Token.ExpiresAt {
		t.Fatal("grant and token expiries differ")
	}
	var before int
	if err := database.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path string
		extra      []string
	}{
		{"existing", tokenPath, nil},
		{"relative", "relative-token", nil},
		{"exclusive", filepath.Join(dir, "exclusive"), []string{"--token", lookup.Token.ID}},
		{"grant-failure", filepath.Join(dir, "failed"), []string{"--owner", agent.ID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{}, base...)
			args[len(args)-1] = test.path
			args = append(args, test.extra...)
			out.Reset()
			if err := runAuditAccess(ctx, args, &out); err == nil {
				t.Fatal("unsafe provisioning succeeded")
			}
			if out.Len() != 0 {
				t.Fatal("failed provisioning printed output")
			}
			var after int
			if err := database.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&after); err != nil || after != before {
				t.Fatal("failed provisioning changed tokens")
			}
			if test.name != "existing" {
				if _, err := os.Lstat(test.path); !os.IsNotExist(err) {
					t.Fatal("failed provisioning left file")
				}
			}
		})
	}
	insecure := filepath.Join(dir, "insecure")
	if err := os.Mkdir(insecure, 0755); err != nil {
		t.Fatal(err)
	}
	args := append([]string{}, base...)
	args[len(args)-1] = filepath.Join(insecure, "new-token")
	if err := runAuditAccess(ctx, args, &out); err == nil {
		t.Fatal("insecure parent accepted")
	}
	unchanged, err := os.ReadFile(tokenPath)
	if err != nil || string(unchanged) != string(raw) {
		t.Fatal("existing token file changed")
	}
	t.Log("synthetic token-file receipt: private file; project read scopes; matching expiry; failed provisioning left no new token")
}

func TestAuditAccessRefusesMissingAndUnmigratedDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "missing.db")
	var out bytes.Buffer
	args := []string{"list", "--db", path, "--project", "synthetic"}
	if err := runAuditAccess(ctx, args, &out); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("database created: %v", err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runAuditAccess(ctx, args, &out); err == nil {
		t.Fatal("unmigrated database accepted")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 0 {
		t.Fatalf("database modified: %v", err)
	}
	out.Reset()
	if err := runAuditAccess(ctx, []string{"--help"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "delegated AI") {
		t.Fatal("help omitted delegated AI purpose")
	}
}
