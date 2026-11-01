import type { Priority } from './core';

export interface CodexAccountStatus {
  connected: boolean;
  account_type?: string;
  email?: string;
  plan_type?: string;
  requires_openai_auth: boolean;
}

export interface CodexDeviceLogin {
  login_id: string;
  verification_url: string;
  user_code: string;
}

export interface TaskDraftSuggestion {
  title: string;
  description: string;
  acceptance_criteria: string[];
  priority: Priority;
  rationale: string;
  supporting_task_keys: string[];
}

export interface ProjectIntelligenceMetrics {
  open_tasks: number;
  completed_tasks: number;
  active_tasks: number;
  blocked_tasks: number;
  overdue_tasks: number;
  due_soon_tasks: number;
  urgent_tasks: number;
  high_priority_tasks: number;
  action_needed: number;
  stale_agent_work: number;
  dependency_blocked: number;
  direct_unblock_count: number;
  severe_bugs: number;
  unread_notifications: number;
  created_7d: number;
  completed_7d: number;
  net_open_change_7d: number;
  planned_focus_count: number;
  focus_due_soon: number;
  oldest_active_days?: number;
  last_activity_at?: string;
}

export type ProjectAttention = 'act_now' | 'watch' | 'steady' | 'quiet';

export interface ProjectIntelligenceRecommendation {
  project_id: string;
  attention: ProjectAttention;
  reason_codes: string[];
  summary: string;
  confidence: 'high' | 'medium' | 'low';
  metrics: ProjectIntelligenceMetrics;
}

export interface ProjectIntelligenceInsight {
  kind: string;
  project_ids: string[];
  summary: string;
}

export interface ProjectIntelligenceResponse {
  as_of: string;
  source: 'deterministic' | 'luna';
  stale: boolean;
  snapshot_hash: string;
  recommendation_version: string;
  projects: ProjectIntelligenceRecommendation[];
  workspace_insights: ProjectIntelligenceInsight[];
}

export type LunaRunOutcome = 'running' | 'succeeded' | 'invalid_output' | 'incomplete' | 'limit_reached' | 'timed_out' | 'canceled' | 'unavailable';

/** Chronological, content-free milestones recorded for a Luna turn. */
export interface LunaRunStep {
  sequence: number;
  kind: string;
  at: string;
}

/** Private, content-free list metadata for one Luna model turn. */
export interface LunaRun {
  id: string;
  project_id?: string;
  project_key?: string;
  feature: 'task_draft' | 'project_intelligence';
  outcome: LunaRunOutcome;
  model: string;
  effort: 'low' | 'medium' | 'high' | 'xhigh' | 'max' | 'ultra';
  thread_id?: string;
  turn_id?: string;
  duration_ms?: number;
  output_bytes?: number;
  detail?: string;
  steps?: LunaRunStep[];
  started_at: string;
  completed_at?: string;
}

/** Bounded model exchange, returned only for the initiating human on detail fetch. */
export interface LunaRunDetail extends LunaRun {
  content_available: boolean;
  input_text?: string;
  output_text?: string;
  input_truncated?: boolean;
  output_truncated?: boolean;
}
