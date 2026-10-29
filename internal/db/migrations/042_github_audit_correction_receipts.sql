-- Operational confirmation authority is deliberately absent from portable history.
-- Additive tables preserve named-column SQL used by retained v41 binaries.
CREATE TABLE github_audit_correction_requests (
 audit_id TEXT NOT NULL REFERENCES github_audit_jobs(audit_id),
 actor_id TEXT NOT NULL REFERENCES actors(id),
 request_key TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 created_at TEXT NOT NULL,
 PRIMARY KEY(audit_id,actor_id,request_key)
);
CREATE TABLE github_audit_correction_receipts (
 audit_id TEXT NOT NULL REFERENCES github_audit_jobs(audit_id),
 finding_id TEXT NOT NULL REFERENCES audit_findings(id),
 actor_id TEXT NOT NULL REFERENCES actors(id),
 request_key TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 action TEXT NOT NULL,
 assessment_hash TEXT NOT NULL,
 source_hash TEXT NOT NULL,
 preview_hash TEXT NOT NULL,
 outcome TEXT NOT NULL CHECK(outcome IN ('applied','already_applied','skipped','failed')),
 result_json TEXT NOT NULL CHECK(json_valid(result_json)),
 created_at TEXT NOT NULL,
 PRIMARY KEY(audit_id,actor_id,request_key,finding_id),
 FOREIGN KEY(audit_id,actor_id,request_key) REFERENCES github_audit_correction_requests(audit_id,actor_id,request_key)
);
CREATE UNIQUE INDEX github_audit_correction_applied_once
 ON github_audit_correction_receipts(finding_id) WHERE outcome='applied';
CREATE TRIGGER github_audit_correction_receipt_immutable
 BEFORE UPDATE ON github_audit_correction_receipts
 BEGIN SELECT RAISE(ABORT,'audit correction receipts are immutable'); END;
