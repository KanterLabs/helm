-- Project releases are an additive planning model.  Existing tasks remain
-- unassigned (NULL release_id); no inferred membership is created here.
CREATE TABLE IF NOT EXISTS releases (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (
        length(trim(name)) > 0 AND length(name) <= 200
    ),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 10000),
    target_date TEXT CHECK (
        target_date IS NULL OR (
            length(target_date) = 10
            AND target_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
            AND date(target_date) = target_date
        )
    ),
    released_at TEXT,
    released_by TEXT REFERENCES actors(id) ON DELETE SET NULL,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS releases_project_name_unique
    ON releases(project_id, lower(name));
CREATE INDEX IF NOT EXISTS releases_project_target_idx
    ON releases(project_id, released_at, target_date, created_at, id);

ALTER TABLE tasks
    ADD COLUMN release_id TEXT REFERENCES releases(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS tasks_project_release_idx
    ON tasks(project_id, release_id, deleted_at, number, id);

-- Direct SQL writers and retained binaries must not be able to attach a task
-- to another project or to a release that has already shipped.  Membership is
-- explicit: changing parents, labels, dates, or dependencies never changes it.
CREATE TRIGGER IF NOT EXISTS releases_validate_task_insert
BEFORE INSERT ON tasks
WHEN NEW.release_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'release_frozen')
    WHERE EXISTS (
        SELECT 1 FROM releases release
        WHERE release.id = NEW.release_id
          AND release.project_id = NEW.project_id
          AND release.released_at IS NOT NULL
    );

    SELECT RAISE(ABORT, 'release_cross_project_or_not_live')
    WHERE NOT EXISTS (
        SELECT 1
        FROM releases release
        WHERE release.id = NEW.release_id
          AND release.project_id = NEW.project_id
          AND release.released_at IS NULL
    );
END;

CREATE TRIGGER IF NOT EXISTS releases_validate_task_assignment
BEFORE UPDATE OF release_id, project_id ON tasks
WHEN NEW.release_id IS NOT OLD.release_id OR NEW.project_id IS NOT OLD.project_id
BEGIN
    SELECT RAISE(ABORT, 'release_frozen')
    WHERE NEW.release_id IS NOT NULL
      AND EXISTS (
          SELECT 1 FROM releases release
          WHERE release.id = NEW.release_id
            AND release.project_id = NEW.project_id
            AND release.released_at IS NOT NULL
      );

    SELECT RAISE(ABORT, 'release_cross_project_or_not_live')
    WHERE NEW.release_id IS NOT NULL
      AND NOT EXISTS (
          SELECT 1
          FROM releases release
          WHERE release.id = NEW.release_id
            AND release.project_id = NEW.project_id
            AND release.released_at IS NULL
      );

    SELECT RAISE(ABORT, 'release_frozen')
    WHERE OLD.release_id IS NOT NULL
      AND EXISTS (
          SELECT 1 FROM releases release
          WHERE release.id = OLD.release_id
            AND release.released_at IS NOT NULL
      )
      AND NEW.release_id IS NOT OLD.release_id;
END;

-- Once a release is shipped, its direct task membership and completed task
-- state are immutable until the release is explicitly reopened by the store.
-- A task may still receive unrelated metadata edits while released.
CREATE TRIGGER IF NOT EXISTS releases_guard_released_task_lifecycle
BEFORE UPDATE OF column_id, completed_at, deleted_at ON tasks
WHEN OLD.release_id IS NOT NULL
 AND EXISTS (
     SELECT 1 FROM releases release
     WHERE release.id = OLD.release_id
       AND release.released_at IS NOT NULL
 )
BEGIN
    SELECT RAISE(ABORT, 'release_frozen')
    WHERE OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL;

    SELECT RAISE(ABORT, 'release_frozen')
    WHERE OLD.deleted_at IS NULL
      AND COALESCE((
          SELECT semantic_state FROM columns
          WHERE id = OLD.column_id AND project_id = OLD.project_id
      ), '') = 'completed'
      AND (
          COALESCE((
              SELECT semantic_state FROM columns
              WHERE id = NEW.column_id AND project_id = NEW.project_id
          ), '') <> 'completed'
          OR NEW.completed_at IS NULL
      );
END;

-- Deletion is an explicit empty-release action.  Count retained soft-deleted
-- task rows as membership too: ON DELETE RESTRICT must never silently turn a
-- historical assignment into an unassigned task.
CREATE TRIGGER IF NOT EXISTS releases_guard_delete
BEFORE DELETE ON releases
WHEN OLD.released_at IS NOT NULL
  OR EXISTS (SELECT 1 FROM tasks WHERE release_id = OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'release_frozen')
    WHERE OLD.released_at IS NOT NULL;

    SELECT RAISE(ABORT, 'release_has_tasks')
    WHERE EXISTS (SELECT 1 FROM tasks WHERE release_id = OLD.id);
END;

-- A release can be marked released only when it has live direct members and
-- every live task in the transitive prerequisite closure is complete.  The
-- recursive CTE is bounded by the dependency migration's cycle guard; UNION
-- also makes this safe if malformed legacy rows are encountered.
CREATE TRIGGER IF NOT EXISTS releases_guard_complete
BEFORE UPDATE OF released_at ON releases
WHEN OLD.released_at IS NULL AND NEW.released_at IS NOT NULL
BEGIN
	SELECT RAISE(ABORT, 'release_incomplete')
	WHERE NOT EXISTS (
	    SELECT 1 FROM tasks
	    WHERE release_id = OLD.id AND deleted_at IS NULL
	);

	SELECT RAISE(ABORT, 'release_dependency_conflict')
	WHERE EXISTS (
	    WITH RECURSIVE required(task_id) AS (
	        SELECT id FROM tasks
            WHERE release_id = OLD.id AND deleted_at IS NULL
            UNION
            SELECT dependency.prerequisite_task_id
            FROM task_dependencies dependency
            JOIN required current ON current.task_id = dependency.task_id
            JOIN tasks dependent ON dependent.id = dependency.task_id
            WHERE dependent.deleted_at IS NULL
        )
	    SELECT 1
	    FROM required
	    JOIN tasks required_task ON required_task.id = required.task_id
	    LEFT JOIN releases other_release
	      ON other_release.id = required_task.release_id
	    WHERE required_task.deleted_at IS NULL
	      AND (
	          required_task.project_id <> OLD.project_id
	          OR (
	              required_task.release_id IS NOT NULL
	              AND required_task.release_id <> OLD.id
	              AND other_release.released_at IS NULL
	              AND (
	                  required_task.completed_at IS NULL
	                  OR NOT EXISTS (
	                      SELECT 1 FROM columns required_column
	                      WHERE required_column.id = required_task.column_id
	                        AND required_column.project_id = required_task.project_id
	                        AND required_column.semantic_state = 'completed'
	                  )
	              )
	          )
	      )
	);

	SELECT RAISE(ABORT, 'release_incomplete')
	WHERE EXISTS (
	    WITH RECURSIVE required(task_id) AS (
            SELECT id FROM tasks
            WHERE release_id = OLD.id AND deleted_at IS NULL
            UNION
            SELECT dependency.prerequisite_task_id
            FROM task_dependencies dependency
            JOIN required current ON current.task_id = dependency.task_id
            JOIN tasks dependent ON dependent.id = dependency.task_id
            WHERE dependent.deleted_at IS NULL
        )
	    SELECT 1
	    FROM required
	    JOIN tasks required_task ON required_task.id = required.task_id
	    LEFT JOIN columns required_column
	      ON required_column.id = required_task.column_id
	     AND required_column.project_id = required_task.project_id
	    WHERE required_task.deleted_at IS NULL
	      AND (
	          required_task.completed_at IS NULL
	          OR required_column.id IS NULL
	          OR required_column.semantic_state <> 'completed'
	      )
	);
END;
