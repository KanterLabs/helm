package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/codexruntime"
	"github.com/KanterLabs/helm/internal/store"
)

// columnAuditAI is the scripted model. Its judgment comes from the captured
// "Scenario:" line only; citations are the real captured evidence IDs.
type columnAuditAI struct{ upstream *columnAuditUpstream }

func (a *columnAuditAI) Account(context.Context, string, bool) (codexruntime.AccountStatus, error) {
	a.upstream.mu.Lock()
	offline := a.upstream.aiOffline
	a.upstream.mu.Unlock()
	return codexruntime.AccountStatus{Connected: !offline, AccountType: "chatgpt", PlanType: "synthetic"}, nil
}
func (a *columnAuditAI) StartDeviceLogin(context.Context, string) (codexruntime.DeviceLogin, error) {
	return codexruntime.DeviceLogin{}, fmt.Errorf("synthetic account already connected")
}
func (a *columnAuditAI) CancelDeviceLogin(context.Context, string, string) (codexruntime.CancelLoginResult, error) {
	return codexruntime.CancelLoginResult{Status: "cancelled"}, nil
}
func (a *columnAuditAI) LogoutAccount(context.Context, string) error { return nil }

func (a *columnAuditAI) DraftEvidence(ctx context.Context, _ string, r codexruntime.RunRequest) (codexruntime.RunResult, error) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.Prompt), &input); err != nil {
		return codexruntime.RunResult{}, err
	}
	if string(input["phase"]) == `"discovery"` {
		return codexruntime.RunResult{Status: "completed", Output: `{"paths":[],"gap":"No additional owner beyond the literal paths"}`}, nil
	}
	a.upstream.mu.Lock()
	a.upstream.inference++
	delay := time.Duration(a.upstream.delayMS) * time.Millisecond
	a.upstream.mu.Unlock()
	// A visible running stage for leave/return and Cancel journeys.
	select {
	case <-ctx.Done():
		return codexruntime.RunResult{}, ctx.Err()
	case <-time.After(delay):
	}
	var e store.GithubAuditEvidence
	if err := json.Unmarshal(input["evidence"], &e); err != nil {
		return codexruntime.RunResult{}, err
	}
	var columns []store.Column
	json.Unmarshal(input["columns"], &columns)
	rows := []map[string]any{}
	for _, task := range e.Tasks {
		rows = append(rows, columnAuditJudgment(task, e, columns))
	}
	raw, _ := json.Marshal(map[string]any{"assessment_version": 2, "assessments": rows})
	return codexruntime.RunResult{Status: "completed", Output: string(raw)}, nil
}

func columnAuditScenario(task store.GithubAuditTask) string {
	for _, line := range strings.Split(task.Description, "\n") {
		if strings.HasPrefix(line, "Scenario: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Scenario: "))
		}
	}
	return "correct"
}

func columnAuditColumn(columns []store.Column, state string) string {
	for _, c := range columns {
		if c.SemanticState == state && c.ArchivedAt == nil {
			return c.ID
		}
	}
	return ""
}

func columnAuditJudgment(task store.GithubAuditTask, e store.GithubAuditEvidence, columns []store.Column) map[string]any {
	scenario := columnAuditScenario(task)
	refs := func(id string) []string {
		out := []string{}
		for _, item := range e.Items {
			if item.SourceRole == "baseline" && item.EvidenceClass == "implementation" && containsString(item.CriterionIDs, id) && containsString(item.TaskIDs, task.ID) {
				out = append(out, item.ID)
			}
		}
		return out
	}
	conditions := []map[string]any{}
	allRefs := []string{}
	for i, c := range task.Conditions {
		result, explanation, gaps := "supported", "The pinned implementation meets this condition.", []string{}
		cited := refs(c.ID)
		switch {
		case scenario == "unknown" || scenario == "blocked-unknown":
			result, explanation, cited = "unknown", "The relevant behavior was not found in the inspected baseline files.", []string{}
			gaps = []string{"No committed implementation of this condition was inspected"}
		case (scenario == "reopen" || scenario == "bug-reopen" || scenario == "partial-active") && i == len(task.Conditions)-1:
			result, explanation = "contradicted", "The pinned source still lacks this behavior."
		}
		if len(cited) == 0 && result != "unknown" {
			result, explanation, gaps = "unknown", "No baseline implementation excerpt was linked to this condition.", []string{"Implementation excerpt unavailable"}
		}
		allRefs = append(allRefs, cited...)
		conditions = append(conditions, map[string]any{"id": c.ID, "text": c.Text, "origin": c.Origin, "result": result, "explanation": explanation, "citation_ids": cited, "gaps": gaps})
	}
	row := map[string]any{"task_id": task.ID, "captured_version": task.Version, "source_column_id": task.ColumnID, "conditions": conditions, "basis": []any{}}
	target := func(placement, state, action, reason string) {
		column := columnAuditColumn(columns, state)
		if placement == "column_correct" {
			column = task.ColumnID
		}
		row["placement"], row["target_column_id"], row["target_semantic_state"], row["lifecycle_action"], row["reason"] = placement, column, state, action, reason
	}
	implementation := []map[string]any{{"kind": "implementation", "explanation": "Committed implementation exists, but required work remains.", "citation_ids": dedupeStrings(allRefs)}}
	switch scenario {
	case "complete", "claimed", "stale":
		target("correction_recommended", "completed", "complete", "Every completion condition is implemented at the audited commit.")
	case "bug-resolve":
		target("correction_recommended", "completed", "bug_resolve", "The committed fix guards the crash described by this bug.")
	case "reopen":
		target("correction_recommended", "active", "reopen", "JSON export is still missing, so this card is not done.")
		row["basis"] = implementation
	case "bug-reopen":
		target("correction_recommended", "active", "bug_reopen", "The committed refresh still drops the remember-me flag.")
		row["basis"] = implementation
	case "partial-active":
		target("column_correct", "active", "none", "Part of the work is committed and the remaining condition is still open.")
		row["basis"] = implementation
	case "unknown", "blocked-unknown":
		row["placement"], row["target_column_id"], row["target_semantic_state"], row["lifecycle_action"] = "unable_to_verify", nil, nil, "none"
		row["reason"] = "The relevant implementation was not inspected within this run's evidence."
	default:
		target("column_correct", task.SemanticState, "none", "Committed work supports the current column.")
	}
	return row
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func dedupeStrings(values []string) []string {
	out := []string{}
	for _, v := range values {
		if !containsString(out, v) {
			out = append(out, v)
		}
	}
	return out
}
