// Column audit v2 wire shapes. Sources of truth:
// docs/AUDIT_ASSESSMENT_CONTRACT.md and docs/AUDIT_CORRECTION_CONTRACT.md.

export type ConditionResult = 'supported' | 'contradicted' | 'unknown';
export type AssessmentPlacement = 'correction_recommended' | 'column_correct' | 'unable_to_verify';
export type LifecycleAction = 'none' | 'complete' | 'reopen' | 'start' | 'block' | 'move' | 'bug_resolve' | 'bug_reopen';

export interface AssessmentCondition {
  id: string;
  text: string;
  origin: 'goal' | 'acceptance_criterion' | 'checklist' | string;
  result: ConditionResult | string;
  explanation: string;
  citation_ids: string[];
  gaps: string[];
}

export interface AssessmentBasis {
  kind: string;
  explanation: string;
  citation_ids: string[];
}

/** Publication-time observation only; the correction preview is authoritative. */
export interface AssessmentEligibility {
  status: 'not_applicable' | 'review_required' | 'blocked' | string;
  reasons: string[];
}

export interface AuditBaselinePin {
  repository: string;
  ref: string;
  sha: string;
  repository_id?: number;
  pull_request?: number;
}

export interface GithubAssessment {
  assessment_version: 2;
  task_id: string;
  captured_version: number;
  source_column_id: string;
  conditions: AssessmentCondition[];
  placement: AssessmentPlacement | string;
  target_column_id: string | null;
  target_semantic_state: string | null;
  lifecycle_action: LifecycleAction | string;
  reason: string;
  basis: AssessmentBasis[];
  task_key: string;
  task_kind: string;
  source_column_name: string;
  source_semantic_state: string;
  baselines: AuditBaselinePin[];
  captured_at: string;
  application_eligibility: AssessmentEligibility;
}

/** Counts from persisted assessments; independent of findings pagination. */
export interface AssessmentSummary {
  checked: number;
  corrections: number;
  correct: number;
  unable_to_verify: number;
}

/** An explicit PR head pinned as one selected repository's baseline. */
export interface AuditBaselineChoice {
  repository: string;
  pull_request: number;
}

export interface CorrectionItemInput {
  finding_id: string;
  resolution?: 'fixed';
  reason?: string;
  confirmation_token?: string;
}

export interface CorrectionRequest {
  assessment_version: 2;
  preview: boolean;
  items: CorrectionItemInput[];
}

/**
 * A committed lifecycle snapshot, not a full Task. Fetch the normal task for
 * anything else; never cast this into a board Task.
 */
export interface CorrectionTaskSnapshot {
  id: string;
  key: string;
  project_id: string;
  kind: string;
  column_id: string;
  version: number;
  position: number;
  completed_at: string | null;
  claimed_by: string | null;
  claim_expires_at: string | null;
}

export type CorrectionOutcome = 'eligible' | 'applied' | 'already_applied' | 'skipped' | 'failed';

export interface CorrectionResult {
  audit_id: string;
  actor_id: string;
  finding_id: string;
  task_id?: string;
  outcome: CorrectionOutcome | string;
  outcome_known: boolean;
  message: string;
  action?: LifecycleAction | string;
  source_column_id?: string;
  target_column_id?: string;
  target_semantic_state?: string;
  captured_version?: number;
  baselines?: AuditBaselinePin[];
  fingerprint?: string;
  required_inputs?: string[];
  /** In-memory only. Never log, persist or export. */
  confirmation_token?: string;
  expires_at?: string;
  request_key?: string;
  task?: CorrectionTaskSnapshot;
}
