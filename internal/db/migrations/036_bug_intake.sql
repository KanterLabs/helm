-- Bugs is an ordinary backlog column. Existing task identities, positions,
-- versions and lifecycle metadata remain untouched. Keep custom columns and
-- their relative order, inserting immediately after the first ordinary backlog.
CREATE TEMP TABLE bug_intake_projects (
    project_id TEXT PRIMARY KEY,
    insertion_position INTEGER NOT NULL
);
INSERT INTO bug_intake_projects(project_id, insertion_position)
SELECT p.id, COALESCE(
    (SELECT c.position + 1 FROM columns c
      WHERE c.project_id = p.id AND c.archived_at IS NULL
        AND c.semantic_state = 'backlog' AND lower(trim(c.name)) <> 'bugs'
      ORDER BY c.position, c.id LIMIT 1),
    (SELECT COALESCE(MAX(c.position) + 1, 0) FROM columns c
      WHERE c.project_id = p.id AND c.archived_at IS NULL))
FROM projects p
WHERE NOT EXISTS (
    SELECT 1 FROM columns c WHERE c.project_id = p.id
      AND c.archived_at IS NULL AND c.semantic_state = 'backlog'
      AND lower(trim(c.name)) = 'bugs'
);

-- Two steps avoid collisions with the unique live-column position index.
-- Temporary positions are below every retained position; only the final
-- placement advances the resource version.
CREATE TEMP TABLE bug_intake_shifted_columns AS
SELECT c.id, c.position AS original_position,
       MIN(0, (SELECT COALESCE(MIN(position), 0) FROM columns))
           - ROW_NUMBER() OVER (ORDER BY c.id) AS temporary_position
FROM columns c JOIN bug_intake_projects p ON p.project_id = c.project_id
WHERE c.archived_at IS NULL AND c.position >= p.insertion_position;
UPDATE columns
SET position = (SELECT temporary_position FROM bug_intake_shifted_columns WHERE id = columns.id)
WHERE id IN (SELECT id FROM bug_intake_shifted_columns);
UPDATE columns
SET position = (SELECT original_position + 1 FROM bug_intake_shifted_columns WHERE id = columns.id),
    version = version + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id IN (SELECT id FROM bug_intake_shifted_columns);

INSERT INTO columns(id, project_id, name, semantic_state, position, created_at, updated_at)
SELECT lower(hex(randomblob(16))), project_id, 'Bugs', 'backlog', insertion_position,
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM bug_intake_projects;
DROP TABLE bug_intake_shifted_columns;
DROP TABLE bug_intake_projects;
