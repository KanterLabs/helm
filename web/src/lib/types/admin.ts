/** Server-authoritative issue health aggregate used by the Issues view. */
export interface IssueMetrics {
  reopened: number;
  window_days: number;
  since: string;
  as_of: string;
}

export interface AdminRequestCounts {
  total: number;
  agent: number;
  human: number;
  anonymous: number;
  errors: number;
}

/** Installation-wide administrator metrics (beta). Request counts are persisted hourly. */
export interface AdminMetrics {
  generated_at: string;
  window_days: number;
  release_sha: string;
  requests: AdminRequestCounts & {
    since: string;
    avg_ms: number;
    hourly: Array<AdminRequestCounts & { hour: string }>;
    daily: Array<AdminRequestCounts & { date: string }>;
    top_actors: Array<{ actor_id: string; name: string; kind: string; requests: number; errors: number; avg_ms: number; last_seen_at: string }>;
  };
  totals: { projects: number; tasks_open: number; tasks_done: number; bugs_open: number; claims_active: number; humans: number; agents: number; active_tokens: number };
  activity: { tasks_created: number; tasks_completed: number; bugs_reported: number; bugs_resolved: number; comments: number; claims: number; progress_updates: number; agent_events: number; human_events: number };
  daily: Array<{ date: string; tasks_created: number; tasks_completed: number; comments: number; agent_events: number; human_events: number }>;
  agents: Array<{ actor_id: string; name: string; disabled: boolean; events: number; claims: number; completions: number; last_event_at: string | null; token_last_used_at: string | null }>;
}

/** Scalar navigation counts; no task rows are returned by this endpoint. */
export interface SidebarCounts {
  issues: number;
  my_work: number;
  view: 'live' | 'assigned';
}

/** A retained, validated beta build exposed to the owner-only control plane. */
export interface BetaBuild {
  sha: string;
  ref: string;
  /** Bounded, single-line Git commit subject supplied by the trusted beta release metadata. */
  subject?: string;
  current?: boolean;
}

/** Optional beta control-plane discovery. Disabled responses stay invisible. */
export interface BetaBuildsResponse {
  enabled: boolean;
  current_sha: string;
  builds: BetaBuild[];
}

/** A durable beta switch/restart job returned by the control plane. */
export interface BetaSwitchJob {
  id: string;
  target_sha: string;
  state: string;
  current_sha?: string;
  error?: string;
  message?: string;
}

/** A beta switch request accepted by the control plane. */
export interface BetaSwitchResponse {
  enabled: boolean;
  job: BetaSwitchJob;
}
