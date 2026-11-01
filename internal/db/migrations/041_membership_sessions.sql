-- Human membership is explicit.  The legacy actors.admin bit remains in place
-- for retained binaries and is projected from workspace_memberships by the
-- current application; it is no longer a Cloudflare privilege source.
CREATE TABLE IF NOT EXISTS workspace_memberships (
    actor_id TEXT PRIMARY KEY REFERENCES actors(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS project_memberships (
    actor_id TEXT NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (actor_id, project_id)
);

CREATE INDEX IF NOT EXISTS workspace_memberships_role_idx
    ON workspace_memberships(role, actor_id);
CREATE INDEX IF NOT EXISTS project_memberships_project_idx
    ON project_memberships(project_id, actor_id);

-- Membership tables are intentionally human-only. SQLite CHECK constraints
-- cannot safely reference another table, so triggers enforce the actor-kind
-- boundary for direct SQL writers as well as the Go store.
CREATE TRIGGER IF NOT EXISTS workspace_memberships_human_insert
BEFORE INSERT ON workspace_memberships
WHEN COALESCE((SELECT kind FROM actors WHERE id = NEW.actor_id), '') <> 'human'
BEGIN
    SELECT RAISE(ABORT, 'workspace memberships require human actors');
END;

CREATE TRIGGER IF NOT EXISTS workspace_memberships_human_update
BEFORE UPDATE OF actor_id ON workspace_memberships
WHEN COALESCE((SELECT kind FROM actors WHERE id = NEW.actor_id), '') <> 'human'
BEGIN
    SELECT RAISE(ABORT, 'workspace memberships require human actors');
END;

CREATE TRIGGER IF NOT EXISTS project_memberships_human_insert
BEFORE INSERT ON project_memberships
WHEN COALESCE((SELECT kind FROM actors WHERE id = NEW.actor_id), '') <> 'human'
BEGIN
    SELECT RAISE(ABORT, 'project memberships require human actors');
END;

CREATE TRIGGER IF NOT EXISTS project_memberships_human_update
BEFORE UPDATE OF actor_id ON project_memberships
WHEN COALESCE((SELECT kind FROM actors WHERE id = NEW.actor_id), '') <> 'human'
BEGIN
    SELECT RAISE(ABORT, 'project memberships require human actors');
END;

CREATE TRIGGER IF NOT EXISTS memberships_actor_kind_guard
BEFORE UPDATE OF kind ON actors
WHEN NEW.kind <> 'human'
 AND (EXISTS (SELECT 1 FROM workspace_memberships WHERE actor_id = NEW.id)
      OR EXISTS (SELECT 1 FROM project_memberships WHERE actor_id = NEW.id))
BEGIN
    SELECT RAISE(ABORT, 'actors with memberships must remain human');
END;

-- Existing humans were already admitted by the pre-membership application.
-- Preserve that access explicitly, while keeping agents in their existing
-- project-ceiling table.  INSERT OR IGNORE makes interrupted/retried upgrades
-- safe and never changes a previously reviewed role.
INSERT OR IGNORE INTO workspace_memberships(actor_id, role, created_at, updated_at)
SELECT id,
       CASE WHEN admin = 1 THEN 'owner' ELSE 'member' END,
       created_at,
       updated_at
FROM actors
WHERE kind = 'human';

-- Before membership, every human reader had access to every existing project.
-- Capture that access explicitly once; future projects require reviewed grants.
INSERT OR IGNORE INTO project_memberships(actor_id, project_id, created_at, updated_at)
SELECT a.id, p.id, a.created_at, a.updated_at
FROM actors a CROSS JOIN projects p
WHERE a.kind = 'human';

-- Sessions retain their opaque token hash but gain operator-visible metadata.
-- Existing rows receive NULL metadata and remain valid to retained binaries.
ALTER TABLE sessions ADD COLUMN revoked_at TEXT;
ALTER TABLE sessions ADD COLUMN ip_address TEXT;
ALTER TABLE sessions ADD COLUMN user_agent TEXT;

CREATE INDEX IF NOT EXISTS sessions_actor_state_idx
    ON sessions(actor_id, revoked_at, expires_at, created_at DESC);
