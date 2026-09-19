-- Hourly API request rollups for the administrator metrics page. The server
-- aggregates requests in memory and adds each batch here about once a minute
-- and on graceful shutdown, so counts survive restarts and deploys.
-- actor_id is '' for unauthenticated requests. It deliberately has no foreign
-- key: rows are aggregate history, pruned after 90 days by the server.
CREATE TABLE request_activity_hourly (
    hour TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    actor_kind TEXT NOT NULL,
    requests INTEGER NOT NULL DEFAULT 0 CHECK (requests >= 0),
    errors INTEGER NOT NULL DEFAULT 0 CHECK (errors >= 0),
    duration_ms_sum INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms_sum >= 0),
    last_seen_at TEXT NOT NULL,
    PRIMARY KEY (hour, actor_id)
);

CREATE INDEX request_activity_hourly_actor_idx
    ON request_activity_hourly(actor_id, hour);
