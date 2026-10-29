package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

func scanPortableArtifact(row interface{ Scan(...any) error }) (PortableGithubArtifact, error) {
	var v PortableGithubArtifact
	err := row.Scan(&v.ID, &v.ProjectID, &v.Repository, &v.RepositoryID, &v.Number, &v.URL, &v.CreatedAt)
	return v, err
}
func scanPortableAuditRun(row interface{ Scan(...any) error }) (PortableAuditRun, error) {
	var v PortableAuditRun
	err := row.Scan(&v.ID, &v.ProjectID, &v.ActorID, &v.Scope, &v.Status, &v.StartedAt, &v.FinalizedAt, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}
func scanPortableAuditFinding(row interface{ Scan(...any) error }) (PortableAuditFinding, error) {
	var v PortableAuditFinding
	var refs string
	var assessment sql.NullString
	err := row.Scan(&v.ID, &v.AuditID, &v.TaskID, &v.CapturedVersion, &v.SourceColumn, &v.Verdict, &v.ProposedSemanticDestination, &v.Confidence, &v.Reason, &refs, &v.ReviewState, &v.Version, &v.CreatedAt, &v.UpdatedAt, &assessment)
	if err == nil && assessment.Valid {
		err = json.Unmarshal([]byte(assessment.String), &v.Assessment)
	}
	if err == nil {
		err = json.Unmarshal([]byte(refs), &v.EvidenceRefs)
	}
	return v, err
}

const portableArtifactColumns = `a.id,a.project_id,a.repository,a.repository_id,a.number,a.url,a.created_at`
const portableAuditRunColumns = `r.id,r.project_id,r.actor_id,r.scope,r.status,r.started_at,r.finalized_at,r.created_at,r.updated_at`
const portableAuditFindingColumns = `f.id,f.audit_id,f.task_id,f.captured_version,f.source_column,f.verdict,f.proposed_semantic_destination,f.confidence,f.reason,f.evidence_refs,f.review_state,f.version,f.created_at,f.updated_at,f.assessment_json`

func exportPortableGithub(ctx context.Context, q portableSQL, archive PortableArchive) (*PortableGithubArchive, error) {
	out := &PortableGithubArchive{Artifacts: []PortableGithubArtifact{}, TaskArtifacts: []PortableTaskArtifact{}, AuditRuns: []PortableAuditRun{}, AuditFindings: []PortableAuditFinding{}, AuditEvidence: []GithubAuditEvidence{}}
	// An empty authorized selection must never become an unrestricted query.
	if len(archive.Projects) == 0 {
		return out, nil
	}
	projects := make([]string, 0, len(archive.Projects))
	for _, p := range archive.Projects {
		projects = append(projects, p.ID)
	}
	where, args := portableIDsClause("a.project_id", projects)
	rows, err := q.QueryContext(ctx, `SELECT `+portableArtifactColumns+` FROM github_artifacts a WHERE 1=1`+where+` ORDER BY a.id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		v, err := scanPortableArtifact(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out.Artifacts = append(out.Artifacts, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	where, args = portableIDsClause("t.project_id", projects)
	rows, err = q.QueryContext(ctx, `SELECT l.task_id,l.artifact_id,l.created_at,l.created_by FROM task_artifacts l JOIN tasks t ON t.id=l.task_id WHERE t.deleted_at IS NULL`+where+` ORDER BY l.task_id,l.artifact_id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v PortableTaskArtifact
		if err = rows.Scan(&v.TaskID, &v.ArtifactID, &v.CreatedAt, &v.CreatedBy); err != nil {
			rows.Close()
			return nil, err
		}
		out.TaskArtifacts = append(out.TaskArtifacts, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	where, args = portableIDsClause("r.project_id", projects)
	rows, err = q.QueryContext(ctx, `SELECT `+portableAuditRunColumns+` FROM audit_runs r WHERE 1=1`+where+` ORDER BY r.id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		v, err := scanPortableAuditRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out.AuditRuns = append(out.AuditRuns, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = q.QueryContext(ctx, `SELECT `+portableAuditFindingColumns+` FROM audit_findings f JOIN audit_runs r ON r.id=f.audit_id JOIN tasks t ON t.id=f.task_id AND t.project_id=r.project_id WHERE t.deleted_at IS NULL`+where+` ORDER BY f.id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		v, err := scanPortableAuditFinding(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out.AuditFindings = append(out.AuditFindings, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = q.QueryContext(ctx, `SELECT r.id,r.project_id,COALESCE(e.evidence_json,j.evidence_json) FROM audit_runs r LEFT JOIN github_audit_retained_evidence e ON e.audit_id=r.id LEFT JOIN github_audit_jobs j ON j.audit_id=r.id WHERE COALESCE(e.evidence_json,j.evidence_json) IS NOT NULL`+where+` ORDER BY r.id`, args...)
	if err != nil {
		return nil, err
	}
	tasks := map[string]PortableTask{}
	for _, task := range archive.Tasks {
		tasks[task.ID] = task
	}
	for rows.Next() {
		var id, projectID, raw string
		if err = rows.Scan(&id, &projectID, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var evidence GithubAuditEvidence
		if err = json.Unmarshal([]byte(raw), &evidence); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode retained audit evidence: %w", err)
		}
		evidence.AuditID = id
		kept := []GithubAuditTask{}
		for _, task := range evidence.Tasks {
			if current, ok := tasks[task.ID]; ok && current.ProjectID == projectID {
				kept = append(kept, task)
			} else {
				evidence.Partial = true
				evidence.Omissions = append(evidence.Omissions, "A captured task is outside the live portable archive")
			}
		}
		evidence.Tasks = kept
		captured := map[string]bool{}
		for _, task := range kept {
			captured[task.ID] = true
		}
		for index := range evidence.Items {
			if evidence.Items[index].TaskIDs == nil {
				continue
			}
			associated := []string{}
			seen := map[string]bool{}
			for _, taskID := range evidence.Items[index].TaskIDs {
				if captured[taskID] && !seen[taskID] {
					associated = append(associated, taskID)
					seen[taskID] = true
				} else {
					evidence.Partial = true
					evidence.Omissions = append(evidence.Omissions, "An evidence task association is unavailable in the live portable archive")
				}
			}
			evidence.Items[index].TaskIDs = associated
		}
		out.AuditEvidence = append(out.AuditEvidence, evidence)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return out, nil
}
