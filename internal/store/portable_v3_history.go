package store

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
)

// A re-export of an imported archive contains destination IDs. Its original
// archive digest can differ even when the stored history is exactly equal.
// Compare the complete retained subtree before treating that as a collision.
func portableV3RunHistoryMatches(ctx context.Context, q portableSQL, run PortableAuditRun, g *PortableGithubArchive, plan *portableImportPlan, target string) (bool, error) {
	want := []PortableAuditFinding{}
	mapping := map[string]string{run.ID: target}
	for _, source := range g.AuditFindings {
		if source.AuditID != run.ID {
			continue
		}
		v := source
		v.Assessment = remapPortableAssessment(source.Assessment, plan)
		v.AuditID = target
		v.TaskID = plan.taskMap[v.TaskID]
		if mapped, ok := plan.columnMap[v.SourceColumn]; ok {
			v.SourceColumn = mapped
		}
		v.EvidenceRefs = append([]string{}, v.EvidenceRefs...)
		for i, ref := range v.EvidenceRefs {
			v.EvidenceRefs[i] = portableV3Reference(ref, plan, mapping)
		}
		want = append(want, v)
	}
	rows, err := q.QueryContext(ctx, `SELECT `+portableAuditFindingColumns+` FROM audit_findings f WHERE f.audit_id=? ORDER BY f.id`, target)
	if err != nil {
		return false, err
	}
	have := []PortableAuditFinding{}
	for rows.Next() {
		v, err := scanPortableAuditFinding(rows)
		if err != nil {
			rows.Close()
			return false, err
		}
		have = append(have, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	sort.Slice(want, func(i, j int) bool { return want[i].ID < want[j].ID })
	if !reflect.DeepEqual(want, have) {
		return false, nil
	}
	var sourceEvidence *GithubAuditEvidence
	for _, e := range g.AuditEvidence {
		if e.AuditID == run.ID {
			copy := e
			sourceEvidence = &copy
		}
	}
	rows, err = q.QueryContext(ctx, `SELECT evidence_json FROM github_audit_retained_evidence WHERE audit_id=?`, target)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return sourceEvidence == nil, rows.Err()
	}
	if sourceEvidence == nil {
		return false, nil
	}
	var raw string
	if err = rows.Scan(&raw); err != nil {
		return false, err
	}
	var existing GithubAuditEvidence
	if err = json.Unmarshal([]byte(raw), &existing); err != nil {
		return false, err
	}
	sourceEvidence.AuditID = target
	remapPortableV3EvidenceTasks(sourceEvidence, plan)
	return reflect.DeepEqual(*sourceEvidence, existing), nil
}

// Association IDs and captured task IDs use the same complete task mapping.
// Copy nested slices so history comparison never rewrites the source archive.
func remapPortableV3EvidenceTasks(e *GithubAuditEvidence, plan *portableImportPlan) {
	if e.Columns != nil {
		e.Columns = append([]Column{}, e.Columns...)
	}
	for i := range e.Columns {
		if id, ok := plan.columnMap[e.Columns[i].ID]; ok {
			e.Columns[i].ID = id
		}
		if id, ok := plan.projectMap[e.Columns[i].ProjectID]; ok {
			e.Columns[i].ProjectID = id
		}
	}
	if e.Tasks != nil {
		e.Tasks = append([]GithubAuditTask{}, e.Tasks...)
	}
	for i := range e.Tasks {
		e.Tasks[i].ID = plan.taskMap[e.Tasks[i].ID]
		if mapped, ok := plan.columnMap[e.Tasks[i].ColumnID]; ok {
			e.Tasks[i].ColumnID = mapped
		}
	}
	if e.Items != nil {
		e.Items = append([]GithubAuditItem{}, e.Items...)
	}
	for i := range e.Items {
		if e.Items[i].TaskIDs != nil {
			e.Items[i].TaskIDs = append([]string{}, e.Items[i].TaskIDs...)
		}
		for j, taskID := range e.Items[i].TaskIDs {
			e.Items[i].TaskIDs[j] = plan.taskMap[taskID]
		}
	}
}
