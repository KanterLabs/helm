-- Operational attempts are local state. Evidence and captured task identity are
-- retained separately from mutable task/PR cache state. Legacy statuses remain.
CREATE TABLE github_audit_jobs (
 audit_id TEXT PRIMARY KEY REFERENCES audit_runs(id) ON DELETE CASCADE,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 actor_id TEXT NOT NULL REFERENCES actors(id) ON DELETE RESTRICT,
 request_key TEXT NOT NULL, fingerprint TEXT NOT NULL,
 stage TEXT NOT NULL DEFAULT 'queued' CHECK(stage IN ('queued','fetching','analyzing','validating','finished')),
 outcome TEXT NOT NULL DEFAULT '' CHECK(outcome IN ('','complete','partial','failed','cancelled','interrupted')),
 selection_version INTEGER NOT NULL,
 provider_generation TEXT NOT NULL DEFAULT '', account_generation TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL, effort TEXT NOT NULL,
 capture_json TEXT NOT NULL CHECK(json_valid(capture_json)),
 evidence_json TEXT CHECK(evidence_json IS NULL OR json_valid(evidence_json)),
 lease_owner TEXT NOT NULL DEFAULT '', lease_until TEXT NOT NULL DEFAULT '', fence INTEGER NOT NULL DEFAULT 0,
 deadline TEXT NOT NULL, task_count INTEGER NOT NULL, captured_files INTEGER NOT NULL DEFAULT 0,
 omitted_files INTEGER NOT NULL DEFAULT 0, message TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '',
 retry_of TEXT REFERENCES audit_runs(id) ON DELETE RESTRICT,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, finished_at TEXT,
 UNIQUE(actor_id,project_id,request_key),
 CHECK((stage='finished' AND outcome<>'' AND finished_at IS NOT NULL) OR (stage<>'finished' AND outcome='' AND finished_at IS NULL))
);
CREATE UNIQUE INDEX github_audit_active_actor_project ON github_audit_jobs(actor_id,project_id) WHERE outcome='';
CREATE INDEX github_audit_queue ON github_audit_jobs(stage,outcome,created_at);
CREATE TABLE github_audit_apply_previews (
 audit_id TEXT NOT NULL REFERENCES github_audit_jobs(audit_id) ON DELETE CASCADE,
 finding_id TEXT NOT NULL REFERENCES audit_findings(id) ON DELETE CASCADE,
 actor_id TEXT NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
 finding_version INTEGER NOT NULL, task_version INTEGER NOT NULL, destination TEXT NOT NULL,
 previewed_at TEXT NOT NULL, applied_at TEXT,
 PRIMARY KEY(audit_id,finding_id,actor_id)
);

-- A retained binary has no knowledge of worker ownership. Keep queued jobs
-- resumable, but reject legacy result writes after the worker has stopped.
-- A live validating worker still relies on the service's actor/fence checks.
CREATE TRIGGER github_audit_worker_finding_insert BEFORE INSERT ON audit_findings
WHEN EXISTS(SELECT 1 FROM github_audit_jobs WHERE audit_id=NEW.audit_id)
 AND NOT EXISTS(SELECT 1 FROM github_audit_jobs WHERE audit_id=NEW.audit_id
  AND stage='validating' AND outcome='' AND lease_owner<>''
  AND julianday(lease_until)>julianday('now') AND julianday(deadline)>julianday('now'))
BEGIN SELECT RAISE(ABORT,'server audit findings require live validating worker'); END;

CREATE TRIGGER github_audit_worker_finalize BEFORE UPDATE OF status ON audit_runs
WHEN NEW.status IN ('complete','partial','failed')
 AND EXISTS(SELECT 1 FROM github_audit_jobs WHERE audit_id=NEW.id AND outcome='')
BEGIN SELECT RAISE(ABORT,'server audit job must settle before run finalization'); END;
