package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Inventory in docs/AUDIT_ASSESSMENT_FAILURES.md precedes implementation.
// Each case creates a real store/capture, validates and publishes atomically.
func TestGithubAssessmentsPlacementAndAtomicValidation(t *testing.T) {
	cases := []struct {
		name, source, target, result string
		bad                          bool
		alter                        func(*GithubAssessmentInput, *GithubAuditEvidence, *GithubAuditCapture)
	}{
		{name: "backlog-done", source: "backlog", target: "completed", result: "supported"},
		{name: "active-done", source: "active", target: "completed", result: "supported"},
		{name: "done-correct", source: "completed", target: "completed", result: "supported"},
		{name: "reopen", source: "completed", target: "active", result: "contradicted"},
		{name: "active-correct", source: "active", target: "active", result: "contradicted"},
		{name: "unstarted-ready", source: "backlog", target: "ready", result: "unknown"},
		{name: "unstarted-backlog", source: "ready", target: "backlog", result: "unknown"},
		{name: "concrete-blocker", source: "active", target: "blocked", result: "unknown"},
		{name: "unknown-done", source: "active", target: "completed", result: "unknown", bad: true},
		{name: "test-only", source: "active", target: "completed", result: "supported", bad: true, alter: func(_ *GithubAssessmentInput, e *GithubAuditEvidence, _ *GithubAuditCapture) {
			e.Items[0].EvidenceClass = "test_source"
		}},
		{name: "promise-only", source: "active", target: "completed", result: "supported", bad: true, alter: func(_ *GithubAssessmentInput, e *GithubAuditEvidence, _ *GithubAuditCapture) {
			e.Items[0].EvidenceClass = "documentation"
		}},
		{name: "pr-only", source: "active", target: "completed", result: "supported", bad: true, alter: func(_ *GithubAssessmentInput, e *GithubAuditEvidence, _ *GithubAuditCapture) {
			e.Items[0].SourceRole = "linked_pr"
			e.Items[0].SHA = "cccccccccccccccccccccccccccccccccccccccc"
		}},
		{name: "cross-card", source: "active", target: "completed", result: "supported", bad: true, alter: func(_ *GithubAssessmentInput, e *GithubAuditEvidence, _ *GithubAuditCapture) {
			e.Items[0].TaskIDs = []string{"other-card"}
		}},
		{name: "cross-criterion", source: "active", target: "completed", result: "supported", bad: true, alter: func(_ *GithubAssessmentInput, e *GithubAuditEvidence, _ *GithubAuditCapture) {
			e.Items[0].CriterionIDs = []string{"other-criterion"}
		}},
		{name: "invented-ref", source: "active", target: "completed", result: "supported", bad: true, alter: func(a *GithubAssessmentInput, _ *GithubAuditEvidence, _ *GithubAuditCapture) {
			a.Conditions[0].CitationIDs = []string{"github:invented"}
		}},
		{name: "inconsistent-action", source: "active", target: "completed", result: "supported", bad: true, alter: func(a *GithubAssessmentInput, _ *GithubAuditEvidence, _ *GithubAuditCapture) {
			a.LifecycleAction = "move"
		}},
		{name: "ambiguous-target", source: "active", target: "completed", result: "supported", bad: true, alter: func(_ *GithubAssessmentInput, _ *GithubAuditEvidence, c *GithubAuditCapture) {
			col := c.Columns[0]
			col.ID = "duplicate-done"
			col.SemanticState = "completed"
			c.Columns = append(c.Columns, col)
		}},
		{name: "missing-completion-meaning", source: "active", target: "completed", result: "supported", bad: true, alter: func(a *GithubAssessmentInput, _ *GithubAuditEvidence, c *GithubAuditCapture) {
			a.Conditions = nil
			c.Tasks[0].Conditions = nil
		}},
		{name: "destination-changed", source: "active", target: "completed", result: "supported"},
		{name: "foreign-claim", source: "active", target: "completed", result: "supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDependencyFixture(t, "ASSESS")
			desc := "Goal: Show dependencies on cards.\nWork status: unstarted\nReadiness: ready\nBlocker: Waiting for vendor approval."
			task, err := f.store.CreateTask(f.ctx, f.project.ID, TaskInput{Title: stringPtrForTest("Synthetic assessment"), Description: &desc}, f.actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.target == "backlog" {
				_, err = f.store.DB.ExecContext(f.ctx, `UPDATE columns SET archived_at='synthetic' WHERE project_id=? AND name='Bugs'`, f.project.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			var source string
			err = f.store.DB.QueryRowContext(f.ctx, `SELECT id FROM columns WHERE project_id=? AND semantic_state=?`, f.project.ID, tc.source).Scan(&source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.store.DB.ExecContext(f.ctx, `UPDATE tasks SET column_id=? WHERE id=?`, source, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "unstarted-backlog" {
				_, err = f.store.DB.ExecContext(f.ctx, `UPDATE tasks SET description=replace(description,'Readiness: ready','Readiness: not_ready') WHERE id=?`, task.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "foreign-claim" {
				_, err = f.store.DB.ExecContext(f.ctx, `UPDATE tasks SET claimed_by=?,claim_expires_at='2099-01-01T00:00:00Z' WHERE id=?`, f.actor.ID, task.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			sel, err := f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			run, _, err := f.store.StartGithubAudit(f.ctx, f.project.ID, f.actor.ID, GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, RequestKey: tc.name, Fingerprint: tc.name})
			if err != nil {
				t.Fatal(err)
			}
			j, err := f.store.ClaimGithubAudit(f.ctx, "test", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			capture, _ := f.store.GetGithubAuditCapture(f.ctx, run.ID)
			if capture.AssessmentVersion != 2 || len(capture.Columns) == 0 {
				t.Fatal("new capture lacks v2/columns")
			}
			ct := capture.Tasks[0]
			c := ct.Conditions[0]
			var target string
			for _, col := range capture.Columns {
				if col.SemanticState == tc.target {
					target = col.ID
				}
			}
			placement, action := "correction_recommended", "move"
			if tc.source == tc.target {
				placement, action = "column_correct", "none"
			} else if tc.source == "completed" {
				action = "reopen"
			} else if tc.target == "completed" {
				action = "complete"
			} else if tc.target == "active" {
				action = "start"
			} else if tc.target == "blocked" {
				action = "block"
			}
			a := GithubAssessmentInput{TaskID: task.ID, CapturedVersion: task.Version, SourceColumnID: source, Conditions: []GithubConditionAssessment{{ID: c.ID, Text: c.Text, Origin: c.Origin, Result: tc.result, Explanation: "Pinned behavior evaluated", CitationIDs: []string{"github:file:1"}, Gaps: []string{}}}, Placement: placement, TargetColumnID: &target, TargetSemanticState: &tc.target, LifecycleAction: action, Reason: "Synthetic placement supported by captured facts", Basis: []GithubAssessmentBasis{}}
			e := GithubAuditEvidence{AuditID: run.ID, CapturedAt: now(), Partial: true, Repositories: []GithubAuditRepository{{Repository: "example/synthetic", Ref: "main", SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RepositoryID: 1}}, Tasks: capture.Tasks, Items: []GithubAuditItem{{ID: "github:file:1", Kind: "file", EvidenceClass: "implementation", SourceRole: "baseline", Repository: "example/synthetic", Ref: "main", SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BlobSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Path: "src/card.go", StartLine: 1, EndLine: 2, Text: "func render() { showDependencies() }", TaskIDs: []string{task.ID}, CriterionIDs: []string{c.ID}}}}
			if tc.target == "active" {
				a.Basis = append(a.Basis, GithubAssessmentBasis{Kind: "implementation", Explanation: "Partial committed implementation", CitationIDs: []string{"github:file:1"}})
			}
			if tc.result == "unknown" {
				a.Conditions[0].CitationIDs = []string{}
				a.Conditions[0].Gaps = []string{"Implementation unknown"}
			}
			if tc.target == "ready" || tc.target == "backlog" || tc.target == "blocked" {
				contextItem := GithubAuditItem{ID: "task:context", Kind: "task_context", EvidenceClass: "task_fact", SourceRole: "task_context", TaskIDs: []string{task.ID}, CriterionIDs: []string{c.ID}, Text: ct.Description}
				e.Items = append(e.Items, contextItem)
				kinds := []string{"unstarted", "ready"}
				if tc.target == "backlog" {
					kinds = []string{"unstarted", "not_ready"}
				}
				if tc.target == "blocked" {
					kinds = []string{"blocker"}
				}
				for _, kind := range kinds {
					a.Basis = append(a.Basis, GithubAssessmentBasis{Kind: kind, Explanation: "Affirmative captured context", CitationIDs: []string{"task:context"}})
				}
			}
			if tc.alter != nil {
				tc.alter(&a, &e, &capture)
				raw, _ := json.Marshal(capture)
				_, err = f.store.DB.ExecContext(f.ctx, `UPDATE github_audit_jobs SET capture_json=? WHERE audit_id=?`, string(raw), run.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "destination-changed" {
				_, err = f.store.DB.ExecContext(f.ctx, `UPDATE columns SET name='Changed Done',version=version+1 WHERE id=?`, target)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = f.store.SaveGithubAuditEvidence(f.ctx, j, e); err != nil {
				t.Fatal(err)
			}
			if err = f.store.HeartbeatGithubAudit(f.ctx, j, "validating"); err != nil {
				t.Fatal(err)
			}
			err = f.store.PublishGithubAssessments(f.ctx, j, []GithubAssessmentInput{a}, "partial", "")
			findings, _, _ := f.store.ListAuditFindings(f.ctx, run.ID, 50, 0)
			if tc.bad {
				if err == nil || len(findings) != 0 {
					t.Fatalf("malformed evidence published atomically: %v %+v", err, findings)
				}
				return
			}
			if err != nil || len(findings) != 1 {
				t.Fatalf("valid assessment rejected: %v %+v", err, findings)
			}
			if findings[0].Assessment == nil || findings[0].Assessment.AssessmentVersion != 2 {
				t.Fatal("lost assessment contract")
			}
			if (tc.name == "foreign-claim" || tc.name == "destination-changed") && findings[0].Assessment.ApplicationEligibility.Status != "blocked" {
				t.Fatal("claim must block application separately")
			}
			summary, _ := f.store.GetAuditRun(f.ctx, run.ID)
			summaryJSON, _ := json.Marshal(summary)
			if !strings.Contains(string(summaryJSON), `"assessment_summary"`) {
				t.Fatal("run lacks placement summary counts")
			}
			after, _ := f.store.GetTask(f.ctx, task.ID)
			if after.Version != task.Version {
				t.Fatal("evaluation mutated task")
			}
			t.Logf("%s => %s/%s; eligibility %s", tc.name, placement, action, findings[0].Assessment.ApplicationEligibility.Status)
		})
	}
}

func TestGithubAssessmentsRequireExactTaskSet(t *testing.T) {
	for _, n := range []int{0, 2} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := newDependencyFixture(t, "SCOPE")
			task := f.task(t, "Synthetic scope")
			sel, _ := f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			run, _, err := f.store.StartGithubAudit(f.ctx, f.project.ID, f.actor.ID, GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, RequestKey: "scope", Fingerprint: "scope"})
			if err != nil {
				t.Fatal(err)
			}
			j, _ := f.store.ClaimGithubAudit(f.ctx, "scope", time.Minute)
			e := GithubAuditEvidence{AuditID: run.ID, Items: []GithubAuditItem{}, Repositories: []GithubAuditRepository{}}
			f.store.SaveGithubAuditEvidence(f.ctx, j, e)
			f.store.HeartbeatGithubAudit(f.ctx, j, "validating")
			a := GithubAssessmentInput{TaskID: task.ID, CapturedVersion: task.Version, SourceColumnID: task.ColumnID, Placement: "unable_to_verify", LifecycleAction: "none", Reason: "No completion meaning", Conditions: []GithubConditionAssessment{}, Basis: []GithubAssessmentBasis{}}
			rows := []GithubAssessmentInput{}
			for i := 0; i < n; i++ {
				rows = append(rows, a)
			}
			if err = f.store.PublishGithubAssessments(f.ctx, j, rows, "partial", ""); err == nil {
				t.Fatal("missing/duplicate result accepted")
			}
			got, _ := f.store.GetAuditRun(f.ctx, run.ID)
			if got.FindingCount != 0 {
				t.Fatal("partial publication")
			}
		})
	}
}
