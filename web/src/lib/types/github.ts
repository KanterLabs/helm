import type { AuditRun } from './audits';
import type { Task } from './tasks';
import type { AuditBaselineChoice, AuditBaselinePin } from './columnAudit';

// GitHub beta wire shapes. Source of truth: docs/GITHUB_BETA_API.md.

export type GithubIntegrationState = 'not_configured' | 'connected' | 'permission_required' | 'unavailable' | 'rate_limited';

export interface GithubIntegration {
  enabled: boolean;
  configured: boolean;
  state: GithubIntegrationState | string;
  message: string;
  allowed_repositories: string[];
  checked_at?: string | null;
}

export interface GithubRepositorySelection {
  repository: string;
  ref: string;
}

export interface ProjectGithubSelection {
  version: number;
  repositories: GithubRepositorySelection[];
}

export type GithubAiState = 'connected' | 'not_connected' | 'disabled' | 'unavailable';

export interface GithubReadiness {
  ready: boolean;
  github: { state: GithubIntegrationState | string; message: string };
  ai: { state: GithubAiState | string; message: string };
  repositories: GithubRepositorySelection[];
}

export type PullRequestChecksState = 'unknown' | 'pending' | 'passing' | 'failing' | 'unavailable';
export type PullRequestReviewState = 'approved' | 'changes_requested' | 'commented' | 'none' | 'unknown';

export interface PullRequestCheck {
  name: string;
  state: string;
  conclusion: string;
  url: string;
  sha: string;
}

export interface PullRequestSnapshot {
  repository_id: number;
  title: string;
  state: 'open' | 'closed' | 'merged' | string;
  draft: boolean;
  head_sha: string;
  base_sha: string;
  merge_sha?: string;
  checks_state: PullRequestChecksState | string;
  checks: PullRequestCheck[];
  review_state: PullRequestReviewState | string;
  fetched_at: string;
  stale: boolean;
  partial: boolean;
}

export interface PullRequestArtifact {
  id: string;
  task_id: string;
  kind: 'pull_request';
  provider: 'github';
  url: string;
  repository: string;
  number: number;
  created_at: string;
  verification: 'unverified' | 'verified' | 'unavailable' | string;
  error_code?: string;
  error_message?: string;
  snapshot?: PullRequestSnapshot;
}

export interface TaskArtifactCollection {
  data: PullRequestArtifact[];
  next_cursor?: string | null;
  task_version: number;
}

export type ArtifactSummaryState = 'unverified' | 'pending' | 'passing' | 'failing' | 'merged' | 'closed' | 'unavailable' | 'unknown';

export interface ArtifactSummary {
  task_id: string;
  count: number;
  label: string;
  state: ArtifactSummaryState | string;
}

export type GithubAuditStage = 'queued' | 'fetching' | 'analyzing' | 'validating' | 'finished';
export type GithubAuditOutcome = '' | 'complete' | 'partial' | 'failed' | 'cancelled' | 'interrupted';

export interface GithubAuditJob {
  audit_id: string;
  project_id: string;
  stage: GithubAuditStage | string;
  outcome: GithubAuditOutcome | string;
  created_at: string;
  updated_at: string;
  finished_at?: string;
  task_count: number;
  captured_files: number;
  omitted_files: number;
  message: string;
  error_code?: string;
  retry_of?: string;
}

/** `states` and `task_ids` are alternative scopes; never send both. */
export interface GithubAuditStartInput {
  /** Explicit PR head for one selected repository; omission keeps the saved ref. */
  baseline?: AuditBaselineChoice;
  repositories?: GithubRepositorySelection[];
  states?: string[];
  task_ids?: string[];
}

export interface GithubAuditStartResult {
  audit: AuditRun;
  job: GithubAuditJob;
}

export interface GithubEvidenceItem {
  id: string;
  kind: 'file' | 'pull_request' | 'check' | string;
  title: string;
  url: string;
  repository: string;
  sha: string;
  blob_sha?: string;
  path?: string;
  start_line?: number;
  end_line?: number;
  text: string;
  task_ids?: string[];
  ref?: string;
  /** `baseline`, `linked_pr` or `task_context`. */
  source_role?: string;
  /** e.g. `implementation`, `test_source`, `observed_check`, `pull_request`, `task_fact`. */
  evidence_class?: string;
  criterion_ids?: string[];
}

/** Task identity as captured by the run, independent of board pagination. */
export interface GithubAuditTask {
  id: string;
  key: string;
  title: string;
  description: string;
  version: number;
  column_id: string;
  semantic_state?: string;
  column_name?: string;
  criteria: string[];
  kind?: string;
}

export interface GithubAuditEvidence {
  audit_id: string;
  captured_at: string;
  partial: boolean;
  omitted_files?: number;
  omissions: string[];
  repositories: AuditBaselinePin[];
  items: GithubEvidenceItem[];
  tasks: GithubAuditTask[];
  assessment_version?: number;
  criterion_gaps?: { task_id: string; criterion_id?: string; source_role: string; path?: string; code: string; message: string }[];
  source_coverage?: { repository: string; ref: string; sha: string; source_role: string; indexed_files: number; inspected_files: number; unread_files: number; index_partial: boolean }[];
  retrieval_budget?: Record<string, number>;
}

export type GithubApplyOutcome = 'eligible' | 'applied' | 'skipped' | 'needs_follow_up';

export interface GithubApplyResult {
  finding_id: string;
  task_id: string;
  outcome: GithubApplyOutcome | string;
  message: string;
  task?: Task;
}
