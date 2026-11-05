-- Additive private evidence metadata; blobs are held outside the web root.
CREATE TABLE task_attachments (
 id TEXT PRIMARY KEY,
 task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 actor_id TEXT NOT NULL REFERENCES actors(id),
 filename TEXT NOT NULL, content_type TEXT NOT NULL, size_bytes INTEGER NOT NULL CHECK(size_bytes>0),
 -- Includes original plus safe PNG thumbnail; zero supports legacy fixtures.
 stored_bytes INTEGER NOT NULL DEFAULT 0 CHECK(stored_bytes>=0),
 sha256 TEXT NOT NULL, width INTEGER NOT NULL, height INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'staged' CHECK(status IN ('staged','published','deleted')),
 caption TEXT NOT NULL DEFAULT '', commit_sha TEXT NOT NULL DEFAULT '', build TEXT NOT NULL DEFAULT '',
 viewport_width INTEGER NOT NULL DEFAULT 0, viewport_height INTEGER NOT NULL DEFAULT 0,
 version INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, deleted_at TEXT,
 ready INTEGER NOT NULL DEFAULT 0 CHECK(ready IN (0,1)),
 idempotency_key TEXT, request_hash TEXT NOT NULL DEFAULT '',
 UNIQUE(actor_id,task_id,idempotency_key)
);
CREATE INDEX task_attachments_task_time ON task_attachments(task_id,created_at,id);
CREATE INDEX task_attachments_project_bytes ON task_attachments(project_id);
CREATE TABLE task_attachment_links (
 attachment_id TEXT PRIMARY KEY REFERENCES task_attachments(id) ON DELETE CASCADE,
 comment_id TEXT REFERENCES comments(id) ON DELETE CASCADE,
 -- Progress IDs identify immutable historical publications, not the replaceable pulse.
 progress_id TEXT,
 created_at TEXT NOT NULL,
 position INTEGER NOT NULL DEFAULT 0 CHECK(position>=0 AND position<4),
 CHECK((comment_id IS NOT NULL) != (progress_id IS NOT NULL))
);
CREATE INDEX task_attachment_links_comment ON task_attachment_links(comment_id);
CREATE INDEX task_attachment_links_progress ON task_attachment_links(progress_id);
