package main

import (
	"encoding/json"

	"github.com/KanterLabs/helm/internal/store"
)

// The fixture recommends scheduling only from affirmative captured task facts.
// The placeholder source and merged PR never establish completion of the goal.
func syntheticAssessments(e store.GithubAuditEvidence) (string, error) {
	rows := []map[string]any{}
	for _, task := range e.Tasks {
		conditions := []map[string]any{}
		for _, condition := range task.Conditions {
			refs := []string{}
			for _, item := range e.Items {
				if item.Kind == "file" && item.SourceRole == "baseline" && includes(item.TaskIDs, task.ID) && includes(item.CriterionIDs, condition.ID) {
					refs = append(refs, item.ID)
				}
			}
			conditions = append(conditions, map[string]any{
				"id": condition.ID, "text": condition.Text, "origin": condition.Origin,
				"result": "unknown", "explanation": "The captured placeholder does not establish delivery of the requested workflow.",
				"citation_ids": refs, "gaps": []string{"Requested workflow completion is not established."},
			})
		}
		facts := []string{}
		for _, item := range e.Items {
			if item.EvidenceClass == "task_fact" && includes(item.TaskIDs, task.ID) {
				facts = append(facts, item.ID)
			}
		}
		placement, action := "unable_to_verify", "none"
		var target any
		basis := []map[string]any{}
		if len(task.Conditions) > 0 && len(facts) > 0 && task.Context.Unstarted && task.Context.Readiness == "ready" {
			placement, action, target = "correction_recommended", "move", "ready"
			if task.SemanticState == "ready" {
				placement, action = "column_correct", "none"
			}
			for _, kind := range []string{"unstarted", "ready"} {
				basis = append(basis, map[string]any{"kind": kind, "explanation": "Affirmative captured task scheduling context.", "citation_ids": facts})
			}
		}
		rows = append(rows, map[string]any{
			"task_id": task.ID, "captured_version": task.Version, "source_column_id": task.ColumnID,
			"conditions": conditions, "placement": placement, "target_semantic_state": target,
			"lifecycle_action": action, "reason": "Scheduling is based on captured work/readiness context, not a claim of completed implementation.", "basis": basis,
		})
	}
	output, err := json.Marshal(map[string]any{"assessment_version": store.GithubAssessmentVersion, "assessments": rows})
	return string(output), err
}

func includes(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
