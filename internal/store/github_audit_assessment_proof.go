package store

import (
	"encoding/json"
	"regexp"
	"strings"
)

var assessmentCheckCondition = regexp.MustCompile(`(?i)(\bCI\b|\bchecks?\b|\btests?\b|\bbuild\b).*(pass|succeed|success|green)|(?:pass|succeed|success|green).*(\bCI\b|\bchecks?\b|\btests?\b|\bbuild\b)`)

func assessmentContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func assessmentText(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= limit
}

// Check provenance before examining source class. A citation can only describe
// its own pinned source, captured task and captured criterion.
func assessmentCitation(e GithubAuditEvidence, task GithubAuditTask, criterion, ref string) (GithubAuditItem, error) {
	for _, item := range e.Items {
		if item.ID == ref {
			if !assessmentContains(item.TaskIDs, task.ID) || (criterion != "" && !assessmentContains(item.CriterionIDs, criterion)) {
				return item, invalid("citation is outside task/criterion scope", nil)
			}
			if item.EvidenceClass == "task_fact" {
				if item.Kind != "task_context" || item.SourceRole != "task_context" {
					return item, invalid("invalid task fact source", nil)
				}
				return item, nil
			}
			pinned := false
			for _, repo := range e.Repositories {
				if item.Repository == repo.Repository && item.SHA == repo.SHA {
					pinned = true
				}
			}
			if item.SourceRole == "baseline" && !pinned {
				return item, invalid("citation does not match selected baseline", nil)
			}
			return item, nil
		}
	}
	return GithubAuditItem{}, invalid("unknown captured citation", nil)
}
func assessmentBaseline(e GithubAuditEvidence, item GithubAuditItem) bool {
	for _, repo := range e.Repositories {
		if repo.Repository == item.Repository && repo.SHA == item.SHA {
			return true
		}
	}
	return false
}
func assessmentImplementation(e GithubAuditEvidence, item GithubAuditItem) bool {
	return item.SourceRole == "baseline" && assessmentBaseline(e, item) && item.Kind == "file" && item.EvidenceClass == "implementation" && item.Path != "" && item.BlobSHA != "" && item.StartLine > 0 && item.EndLine >= item.StartLine && item.Text != ""
}
func assessmentCheck(e GithubAuditEvidence, item GithubAuditItem, result string) bool {
	if item.Kind != "check" || item.EvidenceClass != "observed_check" || !assessmentBaseline(e, item) {
		return false
	}
	var observed struct {
		SHA        string `json:"sha"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	if json.Unmarshal([]byte(item.Text), &observed) != nil || observed.SHA != item.SHA {
		return false
	}
	if result == "supported" {
		return observed.Conclusion == "success"
	}
	return observed.Conclusion == "failure" || observed.Conclusion == "timed_out" || observed.Conclusion == "cancelled" || observed.Conclusion == "action_required"
}

func validateAssessmentConditions(a GithubAssessmentInput, task GithubAuditTask, e GithubAuditEvidence) (supported, contradicted int, err error) {
	if len(a.Conditions) != len(task.Conditions) {
		return 0, 0, invalid("every captured condition requires one assessment", nil)
	}
	conditions := map[string]GithubAuditCondition{}
	for _, c := range task.Conditions {
		conditions[c.ID] = c
	}
	seen := map[string]bool{}
	for _, c := range a.Conditions {
		quote, ok := conditions[c.ID]
		if !ok || seen[c.ID] || quote.Text != c.Text || quote.Origin != c.Origin || quote.TaskVersion != task.Version {
			return 0, 0, invalid("condition identity/quote/origin mismatch", nil)
		}
		seen[c.ID] = true
		if !assessmentText(c.Explanation, 2000) || len(c.CitationIDs) > 100 || len(c.Gaps) > 20 {
			return 0, 0, invalid("invalid criterion explanation/evidence limits", nil)
		}
		for _, gap := range c.Gaps {
			if !assessmentText(gap, 1000) {
				return 0, 0, invalid("invalid criterion gap", nil)
			}
		}
		if c.Result != "supported" && c.Result != "contradicted" && c.Result != "unknown" {
			return 0, 0, invalid("invalid condition result", nil)
		}
		if c.Result == "unknown" && len(c.Gaps) == 0 {
			return 0, 0, invalid("unknown condition must identify its gap", nil)
		}
		if c.Result != "unknown" && len(c.Gaps) > 0 {
			return 0, 0, invalid("conclusive condition cannot retain required gaps", nil)
		}
		proof := false
		refs := map[string]bool{}
		for _, ref := range c.CitationIDs {
			if refs[ref] {
				return 0, 0, invalid("duplicate criterion citation", nil)
			}
			refs[ref] = true
			item, e2 := assessmentCitation(e, task, c.ID, ref)
			if e2 != nil {
				return 0, 0, e2
			}
			if assessmentCheckCondition.MatchString(c.Text) {
				proof = proof || assessmentCheck(e, item, c.Result)
			} else {
				proof = proof || assessmentImplementation(e, item)
			}
		}
		if c.Result != "unknown" {
			for _, gap := range e.CriterionGaps {
				if gap.TaskID == task.ID && gap.CriterionID == c.ID && gap.SourceRole == "baseline" {
					return 0, 0, invalid("conclusive condition retains a relevant baseline gap", nil)
				}
			}
		}
		if c.Result != "unknown" && !proof {
			return 0, 0, invalid("conclusive criterion lacks appropriate baseline proof", nil)
		}
		if c.Result == "supported" {
			supported++
		}
		if c.Result == "contradicted" {
			contradicted++
		}
	}
	return supported, contradicted, nil
}

func validateAssessmentBasis(a GithubAssessmentInput, task GithubAuditTask, e GithubAuditEvidence) (map[string]bool, error) {
	facts := map[string]bool{}
	if len(a.Basis) > 10 {
		return facts, invalid("too many placement facts", nil)
	}
	for _, basis := range a.Basis {
		if facts[basis.Kind] || !assessmentText(basis.Explanation, 2000) || len(basis.CitationIDs) == 0 || len(basis.CitationIDs) > 100 {
			return facts, invalid("invalid/duplicate placement basis", nil)
		}
		appropriate := false
		seen := map[string]bool{}
		for _, ref := range basis.CitationIDs {
			if seen[ref] {
				return facts, invalid("duplicate basis citation", nil)
			}
			seen[ref] = true
			item, err := assessmentCitation(e, task, "", ref)
			if err != nil {
				return facts, err
			}
			switch basis.Kind {
			case "implementation", "work_remaining":
				appropriate = appropriate || assessmentImplementation(e, item)
			case "unstarted":
				appropriate = appropriate || (item.EvidenceClass == "task_fact" && task.Context.Unstarted)
			case "ready", "not_ready":
				appropriate = appropriate || (item.EvidenceClass == "task_fact" && task.Context.Readiness == basis.Kind)
			case "blocker":
				blocked := task.Context.Blocker != ""
				for _, d := range task.Context.Dependencies {
					blocked = blocked || d.SemanticState != "completed"
				}
				appropriate = appropriate || (item.EvidenceClass == "task_fact" && blocked)
			default:
				return facts, invalid("unknown placement basis", nil)
			}
		}
		if !appropriate {
			return facts, invalid("placement basis lacks affirmative captured proof", nil)
		}
		facts[basis.Kind] = true
	}
	return facts, nil
}
