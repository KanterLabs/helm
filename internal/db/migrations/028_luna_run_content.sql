-- Private, actor-scoped Luna inspection content. This table is deliberately
-- separate from luna_runs so the history list remains metadata-only and older
-- rows can report unavailable content when no capture exists.
CREATE TABLE IF NOT EXISTS luna_run_content (
    run_id TEXT PRIMARY KEY REFERENCES luna_runs(id) ON DELETE CASCADE,
    input_text TEXT NOT NULL DEFAULT '' CHECK (length(CAST(input_text AS BLOB)) <= 65536),
    output_text TEXT NOT NULL DEFAULT '' CHECK (length(CAST(output_text AS BLOB)) <= 2097152),
    input_truncated INTEGER NOT NULL DEFAULT 0 CHECK (input_truncated IN (0, 1)),
    output_truncated INTEGER NOT NULL DEFAULT 0 CHECK (output_truncated IN (0, 1)),
    updated_at TEXT NOT NULL
);
