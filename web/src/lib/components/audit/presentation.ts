import type {
  AuditRun as ModuleAuditRun,
  Task
} from '../../types';

export type AuditNoticeKind = 'success' | 'error' | 'info';
export type AuditNavigation = (path: string) => void;
export type AuditTaskUpdate = (task: Task) => void;
export type AuditNotice = (kind: AuditNoticeKind, message: string) => void;

/** Keep collection/detail payload handling testable without mounting the UI. */
export function auditStatusLabel(status: string | undefined): string {
  switch ((status || '').toLowerCase()) {
    case 'queued': return 'Queued';
    case 'running': return 'Running';
    case 'partial': return 'Partial';
    case 'failed': return 'Failed';
    case 'finalized':
    case 'complete': return 'Complete';
    default: return status ? status.replace(/[_-]/g, ' ') : 'Unknown';
  }
}

export function auditFindingCount(audit: ModuleAuditRun): number {
  if (typeof audit.finding_count === 'number') return audit.finding_count;
  if (typeof audit.findings_count === 'number') return audit.findings_count;
  if (audit.findings?.length) return audit.findings.length;
  return Object.values(audit.counts || {}).reduce((total, count) => total + (typeof count === 'number' ? count : 0), 0);
}
