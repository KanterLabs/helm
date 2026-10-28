-- Runtime permissions stay local and retain their identity after token deletion
-- or revocation. They are deliberately absent from portable project exports.
CREATE TABLE github_audit_authorizations (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 agent_id TEXT NOT NULL REFERENCES actors(id) ON DELETE RESTRICT,
 token_id TEXT NOT NULL,
 account_owner_id TEXT NOT NULL REFERENCES actors(id) ON DELETE RESTRICT,
 selection_version INTEGER NOT NULL,
 max_tasks INTEGER NOT NULL CHECK(max_tasks BETWEEN 1 AND 100),
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 revoked_at TEXT
);
CREATE INDEX github_audit_authorization_project ON github_audit_authorizations(project_id,created_at,id);
-- Defaults preserve INSERT statements in retained binaries. An agent stays in
-- actor_id, so an older worker's enabled-human check rejects delegated jobs.
ALTER TABLE github_audit_jobs ADD COLUMN account_owner_id TEXT NOT NULL DEFAULT '';
ALTER TABLE github_audit_jobs ADD COLUMN authorization_id TEXT NOT NULL DEFAULT '';
ALTER TABLE github_audit_jobs ADD COLUMN request_token_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX github_audit_active_owner_project
 ON github_audit_jobs(COALESCE(NULLIF(account_owner_id,''),actor_id),project_id) WHERE outcome='';
