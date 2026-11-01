package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/db"
)

func portableV3Fixture(t *testing.T) (*Store, Actor, Project, Task) {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "portable-v3.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	s := New(database)
	ctx := context.Background()
	actor, err := s.CreateActor(ctx, Actor{Kind: "human", Name: "Synthetic archive owner"}, "synthetic-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, ProjectInput{Key: portableTestString("PV3"), Slug: portableTestString("portable-v3"), Name: portableTestString("Synthetic portable project")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("Synthetic linked task")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, actor, project, task
}

func portableV3Populate(t *testing.T, s *Store, actor Actor, project Project, task Task) {
	t.Helper()
	ctx := context.Background()
	timestamp := now()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO github_artifacts(id,project_id,repository,repository_id,number,url,created_at,verification,snapshot_json) VALUES('artifact-v3',?,'example/synthetic',42,7,'https://github.com/example/synthetic/pull/7',?,'verified','{"private_cache":"CACHE_SENTINEL"}')`, []any{project.ID, timestamp}},
		{`INSERT INTO task_artifacts(task_id,artifact_id,created_at,created_by) VALUES(?,'artifact-v3',?,?)`, []any{task.ID, timestamp, actor.ID}},
		{`INSERT INTO audit_runs(id,project_id,actor_id,scope,status,started_at,finalized_at,created_at,updated_at) VALUES('audit-v3',?,?,'board','complete',?,?,?,?)`, []any{project.ID, actor.ID, timestamp, timestamp, timestamp, timestamp}},
		{`INSERT INTO audit_findings(id,audit_id,task_id,captured_version,source_column,verdict,proposed_semantic_destination,confidence,reason,evidence_refs,review_state,created_at,updated_at) VALUES('finding-v3','audit-v3',?,? ,?,'move_proposed','ready',0.7,'Synthetic retained observation','["github:file-v3"]','approved',?,?)`, []any{task.ID, task.Version, task.ColumnID, timestamp, timestamp}},
	}
	for _, statement := range statements {
		if _, err := s.DB.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	evidence := GithubAuditEvidence{AuditID: "audit-v3", CapturedAt: timestamp, Repositories: []GithubAuditRepository{{Repository: "example/synthetic", Ref: "main", SHA: strings.Repeat("a", 40), RepositoryID: 42}}, Items: []GithubAuditItem{{ID: "github:file-v3", Kind: "file", Title: "Synthetic file", URL: "https://github.com/example/synthetic/blob/" + strings.Repeat("a", 40) + "/README.md", Repository: "example/synthetic", SHA: strings.Repeat("a", 40), Path: "README.md", Text: "Synthetic retained evidence"}}, Tasks: []GithubAuditTask{{ID: task.ID, Title: task.Title, Version: task.Version, ColumnID: task.ColumnID}}, Omissions: []string{}}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO github_audit_retained_evidence(audit_id,evidence_json) VALUES('audit-v3',?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestPortableV3PopulatedRoundTripAndLegacyBoundary(t *testing.T) {
	ctx := context.Background()
	source, actor, project, task := portableV3Fixture(t)
	portableV3Populate(t, source, actor, project, task)
	legacy, err := source.ExportPortableVersion(ctx, []string{project.ID}, 2)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Version != 2 || strings.Contains(string(raw), `"github"`) {
		t.Fatalf("legacy export changed: %s", raw)
	}
	archive, err := source.ExportPortableVersion(ctx, []string{project.ID}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if archive.Github == nil || len(archive.Github.Artifacts) != 1 || len(archive.Github.AuditFindings) != 1 || len(archive.Github.AuditEvidence) != 1 {
		t.Fatalf("missing v3 history: %+v", archive.Github)
	}
	raw, err = json.MarshalIndent(archive, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CACHE_SENTINEL", "synthetic-password-hash", "lease_owner", "request_key", "snapshot_json", "provider_generation"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("export leaked %q", forbidden)
		}
	}
	if capture := os.Getenv("HELM_PORTABLE_CAPTURE"); capture != "" {
		if err = os.WriteFile(capture, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	target, targetActor, _, _ := portableV3Fixture(t)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err = target.ImportPortable(ctx, archive, PortableImportOptions{ActorID: targetActor.ID}); err != nil {
			t.Fatalf("import attempt%d: %v", attempt, err)
		}
	}
	for table, want := range map[string]int{"github_artifacts": 1, "task_artifacts": 1, "audit_runs": 1, "audit_findings": 1, "audit_imports": 1, "github_audit_retained_evidence": 1, "github_audit_jobs": 0, "github_audit_apply_previews": 0} {
		var count int
		if err = target.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count%d want%d err%v", table, count, want, err)
		}
	}
	var verification string
	var cache *string
	if err = target.DB.QueryRowContext(ctx, `SELECT verification,snapshot_json FROM github_artifacts`).Scan(&verification, &cache); err != nil || verification != "unverified" || cache != nil {
		t.Fatalf("import cache %s %v %v", verification, cache, err)
	}
	finding, err := target.GetAuditFinding(ctx, "finding-v3")
	if err != nil {
		t.Fatal(err)
	}
	if !finding.ChangedSinceAudit {
		t.Fatal("imported recommendation incorrectly fresh")
	}
	var evidenceRaw string
	if err = target.DB.QueryRowContext(ctx, `SELECT evidence_json FROM github_audit_retained_evidence`).Scan(&evidenceRaw); err != nil {
		t.Fatal(err)
	}
	var retained GithubAuditEvidence
	if err = json.Unmarshal([]byte(evidenceRaw), &retained); err != nil || retained.AuditID != "audit-v3" || retained.Tasks[0].ID != finding.TaskID {
		t.Fatalf("retained references %+v %v", retained, err)
	}
	t.Logf("synthetic capture: v2=%d v3=%d PRs=%d findings=%d jobs=0", legacy.Version, archive.Version, len(archive.Github.Artifacts), len(archive.Github.AuditFindings))
}

func TestPortableV3RejectsInvalidGraphWithoutWrites(t *testing.T) {
	ctx := context.Background()
	source, actor, project, task := portableV3Fixture(t)
	portableV3Populate(t, source, actor, project, task)
	good, err := source.ExportPortableVersion(ctx, []string{project.ID}, 3)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*PortableArchive){
		"legacy envelope":   func(a *PortableArchive) { a.Version = 2 },
		"dangling task":     func(a *PortableArchive) { a.Github.TaskArtifacts[0].TaskID = "unknown-task" },
		"bad URL":           func(a *PortableArchive) { a.Github.Artifacts[0].URL = "https://attacker.invalid/pull/7" },
		"dangling finding":  func(a *PortableArchive) { a.Github.AuditFindings[0].AuditID = "unknown-run" },
		"bad citation":      func(a *PortableArchive) { a.Github.AuditFindings[0].EvidenceRefs = []string{"javascript:alert"} },
		"bad manifest task": func(a *PortableArchive) { a.Github.AuditEvidence[0].Tasks[0].ID = "unknown-task" },
		"duplicate run":     func(a *PortableArchive) { a.Github.AuditRuns = append(a.Github.AuditRuns, a.Github.AuditRuns[0]) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(good)
			var bad PortableArchive
			if err := json.Unmarshal(raw, &bad); err != nil {
				t.Fatal(err)
			}
			mutate(&bad)
			target, targetActor, _, _ := portableV3Fixture(t)
			var before int
			target.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&before)
			if _, err := target.ImportPortable(ctx, bad, PortableImportOptions{ActorID: targetActor.ID}); err == nil {
				t.Fatal("invalid graph imported")
			}
			var after int
			target.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&after)
			if before != after {
				t.Fatal("partial import wrote projects")
			}
		})
	}
}
