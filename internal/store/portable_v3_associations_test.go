package store

import (
	"context"
	"encoding/json"
	"testing"
)

func portableV3SetAssociations(t *testing.T, s *Store, ids []string) {
	t.Helper()
	ctx := context.Background()
	var raw string
	if err := s.DB.QueryRowContext(ctx, `SELECT evidence_json FROM github_audit_retained_evidence WHERE audit_id='audit-v3'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var e GithubAuditEvidence
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	e.Items[0].TaskIDs = ids
	e.Tasks[0].SemanticState = "backlog"
	e.Tasks[0].ColumnName = "Captured Backlog"
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE github_audit_retained_evidence SET evidence_json=? WHERE audit_id='audit-v3'`, string(data)); err != nil {
		t.Fatal(err)
	}
}

func TestPortableV3EvidenceAssociationsRemapAndRemainStable(t *testing.T) {
	ctx := context.Background()
	source, actor, project, task := portableV3Fixture(t)
	portableV3Populate(t, source, actor, project, task)
	portableV3SetAssociations(t, source, []string{task.ID})
	archive, err := source.ExportPortableVersion(ctx, []string{project.ID}, 3)
	if err != nil {
		t.Fatal(err)
	}
	target, targetActor, _, _ := portableV3Fixture(t)
	legacy, err := source.ExportPortableVersion(ctx, []string{project.ID}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.ImportPortable(ctx, legacy, PortableImportOptions{ActorID: targetActor.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = target.DB.ExecContext(ctx, `UPDATE tasks SET title='Colliding destination task' WHERE id=?`, task.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = target.ImportPortable(ctx, archive, PortableImportOptions{ActorID: targetActor.ID}); err != nil {
			t.Fatal(err)
		}
	}
	var raw, mappedTask string
	if err = target.DB.QueryRowContext(ctx, `SELECT evidence_json FROM github_audit_retained_evidence`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = target.DB.QueryRowContext(ctx, `SELECT task_id FROM audit_findings`).Scan(&mappedTask); err != nil {
		t.Fatal(err)
	}
	var e GithubAuditEvidence
	if err = json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	if mappedTask == task.ID || len(e.Items[0].TaskIDs) != 1 || e.Items[0].TaskIDs[0] != mappedTask {
		t.Fatalf("evidence task association was not remapped: %+v expected%s", e.Items[0].TaskIDs, mappedTask)
	}
	if e.Tasks[0].SemanticState != "backlog" || e.Tasks[0].ColumnName != "Captured Backlog" {
		t.Fatal("captured column identity changed")
	}
	if archive.Github.AuditEvidence[0].Items[0].TaskIDs[0] != task.ID {
		t.Fatal("import mutated caller archive")
	}
	reexport, err := target.ExportPortableVersion(ctx, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.ImportPortable(ctx, reexport, PortableImportOptions{ActorID: targetActor.ID}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = target.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_imports`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("re-export duplicated history: count%d err%v", count, err)
	}
}

func TestPortableV3EvidenceAssociationsRejectInvalidReferences(t *testing.T) {
	ctx := context.Background()
	source, actor, project, task := portableV3Fixture(t)
	portableV3Populate(t, source, actor, project, task)
	other, err := source.CreateProject(ctx, ProjectInput{Key: portableTestString("ASSOC"), Slug: portableTestString("other-association"), Name: portableTestString("Other association project")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherTask, err := source.CreateTask(ctx, other.ID, TaskInput{Title: portableTestString("Other project task")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := source.ExportPortableVersion(ctx, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	for name, ids := range map[string][]string{"unknown": {"unknown-task"}, "cross project": {otherTask.ID}, "duplicate": {task.ID, task.ID}} {
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(archive)
			var bad PortableArchive
			if err := json.Unmarshal(data, &bad); err != nil {
				t.Fatal(err)
			}
			bad.Github.AuditEvidence[0].Items[0].TaskIDs = ids
			target, targetActor, _, _ := portableV3Fixture(t)
			if _, err := target.ImportPortable(ctx, bad, PortableImportOptions{ActorID: targetActor.ID}); err == nil {
				t.Fatal("invalid evidence task association imported")
			}
			var count int
			if err := target.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_runs`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("invalid import wrote history: %d %v", count, err)
			}
		})
	}
}

func TestPortableV3ExportFiltersUnavailableEvidenceAssociations(t *testing.T) {
	ctx := context.Background()
	source, actor, project, task := portableV3Fixture(t)
	portableV3Populate(t, source, actor, project, task)
	portableV3SetAssociations(t, source, []string{task.ID, "deleted-source-task"})
	archive, err := source.ExportPortableVersion(ctx, []string{project.ID}, 3)
	if err != nil {
		t.Fatal(err)
	}
	e := archive.Github.AuditEvidence[0]
	if len(e.Items[0].TaskIDs) != 1 || e.Items[0].TaskIDs[0] != task.ID || !e.Partial || len(e.Omissions) == 0 {
		t.Fatalf("export retained unavailable association: %+v", e)
	}
}
