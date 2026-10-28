package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Failure inventory: portable history silently drops assessment JSON or maps
// only the legacy source column; imported history can look live/applicable.
func TestGithubAssessmentPortableHistoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, actor, p, task := portableV3Fixture(t)
	portableV3Populate(t, s, actor, p, task)
	state, target := "ready", ""
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM columns WHERE project_id=? AND semantic_state='ready'`, p.ID).Scan(&target); err != nil {
		t.Fatal(err)
	}
	a := GithubAssessment{GithubAssessmentInput: GithubAssessmentInput{TaskID: task.ID, CapturedVersion: task.Version, SourceColumnID: task.ColumnID, Placement: "correction_recommended", TargetColumnID: &target, TargetSemanticState: &state, LifecycleAction: "move", Reason: "Synthetic retained observation", Conditions: []GithubConditionAssessment{}, Basis: []GithubAssessmentBasis{}}, AssessmentVersion: 2, TaskKey: task.Key, TaskKind: "task", Baselines: []GithubAuditRepository{}, ApplicationEligibility: GithubAssessmentEligibility{Status: "review_required", Reasons: []string{"human_confirmation_required"}}}
	condition := GithubAuditCondition{ID: "criterion:portable", Text: "Show dependencies", Origin: "goal", TaskVersion: task.Version}
	a.Conditions = []GithubConditionAssessment{{ID: condition.ID, Text: condition.Text, Origin: condition.Origin, Result: "unknown", Explanation: "Unstarted task context", CitationIDs: []string{}, Gaps: []string{"Source work is unverified"}}}
	contextID := "task:context:" + task.ID
	a.Basis = []GithubAssessmentBasis{{Kind: "unstarted", Explanation: "Explicit declaration", CitationIDs: []string{contextID}}, {Kind: "ready", Explanation: "Ready declaration", CitationIDs: []string{contextID}}}
	cols, err := s.ListColumns(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	e := GithubAuditEvidence{AssessmentVersion: 2, AuditID: "audit-v3", CapturedAt: now(), Repositories: []GithubAuditRepository{}, Columns: cols, Tasks: []GithubAuditTask{{ID: task.ID, Key: task.Key, Title: task.Title, Version: task.Version, ColumnID: task.ColumnID, SemanticState: "backlog", Kind: "task", Conditions: []GithubAuditCondition{condition}, Context: GithubAuditContext{Unstarted: true, Readiness: "ready", Dependencies: []GithubAuditDependency{}}}}, Items: []GithubAuditItem{{ID: contextID, Kind: "task_context", SourceRole: "task_context", EvidenceClass: "task_fact", TaskIDs: []string{task.ID}, CriterionIDs: []string{condition.ID}, Text: "Synthetic captured context"}}, Omissions: []string{}}
	eraw, _ := json.Marshal(e)
	if _, err = s.DB.ExecContext(ctx, `UPDATE github_audit_retained_evidence SET evidence_json=? WHERE audit_id='audit-v3'`, string(eraw)); err != nil {
		t.Fatal(err)
	}
	refs, _ := json.Marshal([]string{contextID})
	if _, err = s.DB.ExecContext(ctx, `UPDATE audit_findings SET evidence_refs=? WHERE id='finding-v3'`, string(refs)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(a)
	if _, err := s.DB.ExecContext(ctx, `UPDATE audit_findings SET assessment_json=?,confidence=0 WHERE id='finding-v3'`, string(raw)); err != nil {
		t.Fatal(err)
	}
	archive, err := s.ExportPortableVersion(ctx, []string{p.ID}, 3)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(archive)
	if !strings.Contains(string(raw), `"assessment_version":2`) {
		t.Fatal("export dropped v2 assessment")
	}
	for _, mode := range []string{"destination", "refs", "confidence"} {
		var malformed PortableArchive
		if err = json.Unmarshal(raw, &malformed); err != nil {
			t.Fatal(err)
		}
		switch mode {
		case "destination":
			bad := "backlog"
			malformed.Github.AuditFindings[0].ProposedSemanticDestination = &bad
		case "refs":
			malformed.Github.AuditFindings[0].EvidenceRefs = []string{"github:invented"}
		case "confidence":
			malformed.Github.AuditFindings[0].Confidence = 0.9
		}
		isolated, isolatedActor, _, _ := portableV3Fixture(t)
		if _, err = isolated.ImportPortable(ctx, malformed, PortableImportOptions{ActorID: isolatedActor.ID}); err == nil {
			t.Fatalf("inconsistent v2 legacy projection accepted: %s", mode)
		}
		var count int
		isolated.DB.QueryRowContext(ctx, `SELECT count(*) FROM audit_runs`).Scan(&count)
		if count != 0 {
			t.Fatal("malformed import partially wrote history")
		}
	}
	targetStore, targetActor, _, _ := portableV3Fixture(t)
	for i := 0; i < 2; i++ {
		if _, err = targetStore.ImportPortable(ctx, archive, PortableImportOptions{ActorID: targetActor.ID}); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := targetStore.GetAuditFinding(ctx, "finding-v3")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Assessment == nil || restored.Assessment.AssessmentVersion != 2 || !restored.ChangedSinceAudit {
		t.Fatal("lost assessment or imported drift")
	}
	if restored.Assessment.SourceColumnID != restored.SourceColumn || restored.Assessment.TaskID != restored.TaskID {
		t.Fatal("inconsistent remapped assessment")
	}
	os.MkdirAll("/tmp/helm-tc308-handoff", 0700)
	os.WriteFile("/tmp/helm-tc308-handoff/portable-assessment.json", raw, 0600)
}
