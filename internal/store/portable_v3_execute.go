package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
)

func executePortableGithub(ctx context.Context, tx *sql.Tx, plan *portableImportPlan) error {
	for _, record := range plan.github.artifacts {
		if !record.create {
			continue
		}
		v := record.value
		if _, err := tx.ExecContext(ctx, `INSERT INTO github_artifacts(id,project_id,repository,repository_id,number,url,created_at) VALUES(?,?,?,?,?,?,?)`, v.ID, v.ProjectID, v.Repository, v.RepositoryID, v.Number, v.URL, v.CreatedAt); err != nil {
			return err
		}
	}
	for _, record := range plan.github.links {
		if !record.create {
			continue
		}
		v := record.value
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_artifacts(task_id,artifact_id,created_at,created_by) VALUES(?,?,?,?)`, v.TaskID, v.ArtifactID, v.CreatedAt, portableStringArg(v.CreatedBy)); err != nil {
			return err
		}
	}
	for _, record := range plan.github.runs {
		if !record.create {
			continue
		}
		v := record.value
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_runs(id,project_id,actor_id,scope,status,started_at,finalized_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, v.ID, v.ProjectID, v.ActorID, v.Scope, v.Status, v.StartedAt, portableStringArg(v.FinalizedAt), v.CreatedAt, v.UpdatedAt); err != nil {
			return err
		}

	}
	for _, record := range plan.github.findings {
		if !record.create {
			continue
		}
		v := record.value
		refs := append([]string{}, v.EvidenceRefs...)
		for i, sourceCursor := range plan.github.eventRefs[v.ID] {
			if mapped, ok := plan.eventCursorMap[sourceCursor]; ok {
				refs[i] = "events:" + strconv.FormatInt(mapped, 10)
			} else {
				refs[i] = "unavailable:events:" + strconv.FormatInt(sourceCursor, 10)
			}
		}
		raw, err := json.Marshal(refs)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO audit_findings(id,audit_id,task_id,captured_version,source_column,verdict,proposed_semantic_destination,confidence,reason,evidence_refs,review_state,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.AuditID, v.TaskID, v.CapturedVersion, v.SourceColumn, v.Verdict, portableStringArg(v.ProposedSemanticDestination), v.Confidence, v.Reason, string(raw), v.ReviewState, v.Version, v.CreatedAt, v.UpdatedAt); err != nil {
			return err
		}
	}
	for _, e := range plan.github.evidence {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO github_audit_retained_evidence(audit_id,evidence_json) VALUES(?,?) ON CONFLICT(audit_id) DO NOTHING`, e.AuditID, string(raw)); err != nil {
			return err
		}
	}
	for _, record := range plan.github.runs {
		if !record.create {
			continue
		}
		v := record.value
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_imports(audit_id,imported_at,archive_digest) VALUES(?,?,?)`, v.ID, now(), plan.github.digests[v.ID]); err != nil {
			return err
		}
	}
	return nil
}
