package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPortableV3RemapsConflictsAndPreservesProjectCeiling(t *testing.T) {
	ctx := context.Background()
	source, actor, project, task := portableV3Fixture(t)
	portableV3Populate(t, source, actor, project, task)
	other, err := source.CreateProject(ctx, ProjectInput{Key: portableTestString("OTHER"), Slug: portableTestString("private-other"), Name: portableTestString("Outside export ceiling")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.DB.ExecContext(ctx, `INSERT INTO github_artifacts(id,project_id,repository,number,url,created_at) VALUES('outside-artifact',?,'example/private',9,'https://github.com/example/private/pull/9',?)`, other.ID, now()); err != nil {
		t.Fatal(err)
	}
	archive, err := source.ExportPortableVersion(ctx, []string{project.ID}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Projects) != 1 || len(archive.Github.Artifacts) != 1 {
		t.Fatal("export crossed project ceiling")
	}
	target, targetActor, targetProject, _ := portableV3Fixture(t)
	legacy, err := source.ExportPortableVersion(ctx, []string{project.ID}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.ImportPortable(ctx, legacy, PortableImportOptions{ActorID: targetActor.ID, TargetProjectID: targetProject.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = target.DB.ExecContext(ctx, `UPDATE tasks SET title='Destination identity collision' WHERE id=?`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = target.DB.ExecContext(ctx, `INSERT INTO github_artifacts(id,project_id,repository,number,url,created_at) VALUES('artifact-v3',?,'example/collision',99,'https://github.com/example/collision/pull/99',?)`, targetProject.ID, now()); err != nil {
		t.Fatal(err)
	}
	if _, err = target.DB.ExecContext(ctx, `INSERT INTO audit_runs(id,project_id,actor_id,scope,status,started_at,finalized_at,created_at,updated_at) VALUES('audit-v3',?,?,'collision','complete',?,?,?,?)`, targetProject.ID, targetActor.ID, now(), now(), now(), now()); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err = target.ImportPortable(ctx, archive, PortableImportOptions{ActorID: targetActor.ID, TargetProjectID: targetProject.ID}); err != nil {
			t.Fatalf("attempt%d: %v", attempt, err)
		}
	}
	var mappedTask, mappedAudit, mappedArtifact string
	if err = target.DB.QueryRowContext(ctx, `SELECT f.task_id,f.audit_id FROM audit_findings f JOIN audit_imports i ON i.audit_id=f.audit_id`).Scan(&mappedTask, &mappedAudit); err != nil {
		t.Fatal(err)
	}
	if mappedTask == task.ID || mappedAudit == "audit-v3" {
		t.Fatal("colliding audit/task IDs were not remapped")
	}
	if err = target.DB.QueryRowContext(ctx, `SELECT artifact_id FROM task_artifacts WHERE task_id=?`, mappedTask).Scan(&mappedArtifact); err != nil || mappedArtifact == "artifact-v3" {
		t.Fatalf("artifact reference not remapped: %s %v", mappedArtifact, err)
	}
	var raw string
	if err = target.DB.QueryRowContext(ctx, `SELECT evidence_json FROM github_audit_retained_evidence WHERE audit_id=?`, mappedAudit).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var evidence GithubAuditEvidence
	if err = json.Unmarshal([]byte(raw), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.AuditID != mappedAudit || evidence.Tasks[0].ID != mappedTask {
		t.Fatalf("manifest references lost %+v", evidence)
	}
	var count int
	target.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_imports`).Scan(&count)
	if count != 1 {
		t.Fatalf("retry duplicated imported audit: %d", count)
	}
	if _, err = source.ExportPortableVersion(ctx, nil, 99); err == nil {
		t.Fatal("unsupported export version accepted")
	}
}

// Retained binaries do not know audit_imports. The additive schema must enforce
// the same imported-history boundary for their otherwise valid SQL writes.
func TestPortableV3ImportedHistoryDatabaseGuards(t *testing.T) {
	ctx := context.Background()
	source, actor, project, task := portableV3Fixture(t)
	portableV3Populate(t, source, actor, project, task)
	archive, err := source.ExportPortableVersion(ctx, []string{project.ID}, 3)
	if err != nil {
		t.Fatal(err)
	}
	target, targetActor, _, _ := portableV3Fixture(t)
	if _, err = target.ImportPortable(ctx, archive, PortableImportOptions{ActorID: targetActor.ID}); err != nil {
		t.Fatal(err)
	}
	for name, statement := range map[string]string{
		"finding edit":   `UPDATE audit_findings SET reason='Retained binary edit' WHERE id='finding-v3'`,
		"run transition": `UPDATE audit_runs SET status='partial' WHERE id='audit-v3'`,
	} {
		if _, err = target.DB.ExecContext(ctx, statement); err == nil {
			t.Errorf("imported history accepted %s", name)
		}
	}
}
