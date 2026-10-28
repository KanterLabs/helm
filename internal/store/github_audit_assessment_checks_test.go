package store

import "testing"

// Failure inventory: source tests/README, a stale passing check, or a skipped
// required check can be mistaken for proof; retrieval gaps can be ignored.
func TestGithubAssessmentCommitChecksAndRelevantGaps(t *testing.T) {
	c := GithubAuditCondition{ID: "criterion:checks", Text: "CI checks pass", Origin: "acceptance_criterion", TaskVersion: 1}
	task := GithubAuditTask{ID: "t", Version: 1, ColumnID: "active", SemanticState: "active", Conditions: []GithubAuditCondition{c}}
	capture := GithubAuditCapture{AssessmentVersion: 2, Tasks: []GithubAuditTask{task}, Columns: []Column{{ID: "active", SemanticState: "active"}, {ID: "done", SemanticState: "completed"}}}
	state, target := "completed", "done"
	a := GithubAssessmentInput{TaskID: "t", CapturedVersion: 1, SourceColumnID: "active", Conditions: []GithubConditionAssessment{{ID: c.ID, Text: c.Text, Origin: c.Origin, Result: "supported", Explanation: "Observed required CI", CitationIDs: []string{"check:1"}}}, Placement: "correction_recommended", TargetColumnID: &target, TargetSemanticState: &state, LifecycleAction: "complete", Reason: "Required check passed"}
	for _, tc := range []struct {
		name, sha, conclusion string
		gap                   bool
		ok                    bool
	}{{"same-commit", "baseline", "success", false, true}, {"other-commit", "pr-head", "success", false, false}, {"skipped-is-not-passed", "baseline", "skipped", false, false}, {"failure", "baseline", "failure", false, false}, {"required-gap", "baseline", "success", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			e := GithubAuditEvidence{Repositories: []GithubAuditRepository{{Repository: "r", SHA: "baseline"}}, Items: []GithubAuditItem{{ID: "check:1", Kind: "check", EvidenceClass: "observed_check", SourceRole: "linked_pr", Repository: "r", SHA: tc.sha, TaskIDs: []string{"t"}, CriterionIDs: []string{c.ID}, Text: `{"sha":"` + tc.sha + `","conclusion":"` + tc.conclusion + `"}`}}}
			if tc.gap {
				e.CriterionGaps = []GithubAuditCriterionGap{{TaskID: "t", CriterionID: c.ID, SourceRole: "baseline", Code: "source_reference_unresolved", Message: "Required behavior not inspected"}}
			}
			_, _, err := validateGithubAssessment(a, capture, e)
			if (err == nil) != tc.ok {
				t.Fatalf("commit/gap validation got %v expected success %v", err, tc.ok)
			}
		})
	}
}
