-- Explicit selections and durable links are distinct from disposable cache.
CREATE TABLE github_project_settings (
 project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
 version INTEGER NOT NULL DEFAULT 1,
 updated_at TEXT NOT NULL
);
CREATE TABLE github_project_repositories (
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 repository TEXT NOT NULL COLLATE NOCASE,
 ref TEXT NOT NULL,
 PRIMARY KEY(project_id, repository, ref)
);
CREATE TABLE github_artifacts (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 repository TEXT NOT NULL COLLATE NOCASE,
 repository_id INTEGER,
 number INTEGER NOT NULL CHECK(number > 0),
 url TEXT NOT NULL,
 created_at TEXT NOT NULL,
 verification TEXT NOT NULL DEFAULT 'unverified',
 snapshot_json TEXT,
 cache_selection_version INTEGER,
 error_code TEXT NOT NULL DEFAULT '',
 refresh_after TEXT NOT NULL DEFAULT '',
 UNIQUE(project_id, repository, number)
);
CREATE UNIQUE INDEX github_artifacts_identity ON github_artifacts(project_id,repository_id,number) WHERE repository_id IS NOT NULL;
CREATE TABLE task_artifacts (
 task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 artifact_id TEXT NOT NULL REFERENCES github_artifacts(id) ON DELETE CASCADE,
 created_at TEXT NOT NULL,
 created_by TEXT REFERENCES actors(id) ON DELETE SET NULL,
 PRIMARY KEY(task_id,artifact_id)
);
CREATE INDEX task_artifacts_artifact ON task_artifacts(artifact_id,task_id);
CREATE TRIGGER task_artifacts_project_guard BEFORE INSERT ON task_artifacts
WHEN NOT EXISTS(SELECT 1 FROM tasks t JOIN github_artifacts a ON a.id=NEW.artifact_id WHERE t.id=NEW.task_id AND t.project_id=a.project_id AND t.deleted_at IS NULL)
BEGIN SELECT RAISE(ABORT,'artifact cross project'); END;
