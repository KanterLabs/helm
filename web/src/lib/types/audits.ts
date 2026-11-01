import type { Column, SemanticState } from './core';
import type { Task } from './tasks';

/** A server-owned board audit lifecycle state. */
export type AuditRunStatus = 'queued' | 'running' | 'complete' | 'partial' | 'failed' | 'finalized';

export type AuditTerminalStatus = 'complete' | 'partial' | 'failed';

export type AuditVerdict = 'correct' | 'needs_attention' | 'move_proposed';

export type AuditReviewState = 'pending' | 'approved' | 'dismissed';

/**
 * Append-only snapshot metadata returned by the audit collection endpoint.
 * The API may include aggregate counters on list responses and findings on a
 * detail response, so those fields are intentionally optional.
 */
export interface AuditRun {
  id: string;
  project_id: string;
  actor_id?: string;
  scope?: string;
  status: AuditRunStatus | string;
  started_at?: string;
  finalized_at?: string | null;
  created_at?: string;
  updated_at?: string;
  finding_count?: number;
  findings_count?: number;
  counts?: Partial<Record<AuditVerdict, number>> & Record<string, number>;
  findings?: AuditFinding[];
}

/** A finding captured from one task during a board audit. */
export interface AuditFinding {
  id: string;
  audit_id: string;
  task_id: string;
  captured_version: number;
  source_column: string | Pick<Column, 'id' | 'name' | 'semantic_state'>;
  verdict: AuditVerdict | string;
  proposed_semantic_destination?: SemanticState | null;
  confidence: number;
  reason: string;
  evidence_refs?: string[];
  evidence?: string[];
  review_state: AuditReviewState | string;
  version?: number;
  current_version?: number;
  current_column?: string | Pick<Column, 'id' | 'name' | 'semantic_state'>;
  current_column_id?: string;
  changed_since_audit?: boolean;
  current_task?: Task;
  task?: Task;
  current?: {
    version?: number;
    column_id?: string;
    column?: string | Pick<Column, 'id' | 'name' | 'semantic_state'>;
    task?: Task;
  };
  created_at?: string;
  updated_at?: string;
}

export interface AuditDetail extends AuditRun {
  findings: AuditFinding[];
}

export interface AuditFindingPatch {
  review_state: AuditReviewState;
  proposed_semantic_destination?: SemanticState | null;
}
