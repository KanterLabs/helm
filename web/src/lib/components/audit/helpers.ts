import { ApiError, type GithubAuditTask, type AuditRun, type AuditFinding, type AuditDetail, type AuditVerdict, type AuditReviewState, type SemanticState, type Column, type Task } from '../../types';

export const stateLabels: Record<SemanticState, string> = {
  backlog: 'Backlog',
  ready: 'Ready',
  active: 'In progress',
  blocked: 'Blocked',
  completed: 'Done'
};

export const verdictLabels: Record<AuditVerdict, string> = {
  correct: 'Looks correct',
  needs_attention: 'Needs attention',
  move_proposed: 'Move proposed'
};

export const reviewLabels: Record<AuditReviewState, string> = {
  pending: 'Pending review',
  approved: 'Approved',
  dismissed: 'Dismissed'
};

export const semanticStates: SemanticState[] = ['backlog', 'ready', 'active', 'blocked', 'completed'];

export const verdicts: AuditVerdict[] = ['correct', 'needs_attention', 'move_proposed'];

interface AuditContext {
  currentProjectId: string;
  selectedAuditId: string;
  selectedAudit: AuditDetail | null;
  tasks: Task[];
  columns: Column[];
  /** Task identity captured by a server-run audit, keyed by task ID. */
  captured?: Map<string, GithubAuditTask>;
}

export function createAuditHelpers(context: AuditContext) {
  const { currentProjectId, selectedAuditId, selectedAudit, tasks, columns, captured } = context;

  function normalizeAudit(value: unknown): AuditRun {
    const raw = unwrapPayload(value) as Partial<AuditRun>;
    return {
      ...raw,
      id: String(raw.id || ''),
      project_id: String(raw.project_id || currentProjectId),
      status: raw.status || 'unknown'
    } as AuditRun;
  }

  function normalizeFinding(value: unknown): AuditFinding {
    const raw = unwrapPayload(value) as Partial<AuditFinding>;
    return {
      ...raw,
      id: String(raw.id || ''),
      audit_id: String(raw.audit_id || selectedAuditId),
      task_id: String(raw.task_id || ''),
      captured_version: numberOr(raw.captured_version, 0),
      source_column: raw.source_column || '',
      verdict: raw.verdict || 'needs_attention',
      confidence: numberOr(raw.confidence, 0),
      reason: String(raw.reason || ''),
      review_state: raw.review_state || 'pending'
    } as AuditFinding;
  }

  function normalizeDetail(value: unknown): AuditDetail {
    const raw = unwrapPayload(value) as Partial<AuditDetail> & { data?: unknown; findings?: unknown };
    const rawFindings = Array.isArray(raw.findings)
      ? raw.findings
      : Array.isArray((raw.data as { findings?: unknown[] } | undefined)?.findings)
        ? (raw.data as { findings: unknown[] }).findings
        : [];
    return {
      ...raw,
      id: String(raw.id || selectedAuditId),
      project_id: String(raw.project_id || currentProjectId),
      status: raw.status || 'unknown',
      findings: rawFindings.map(normalizeFinding)
    } as AuditDetail;
  }

  function unwrapPayload(value: unknown): unknown {
    if (!value || typeof value !== 'object') return value;
    const record = value as Record<string, unknown>;
    if (record.audit && typeof record.audit === 'object') return record.audit;
    if (record.finding && typeof record.finding === 'object') return record.finding;
    if (record.data && !Array.isArray(record.data) && typeof record.data === 'object') return record.data;
    return value;
  }

  function numberOr(value: unknown, fallback: number): number {
    return typeof value === 'number' && Number.isFinite(value) ? value : fallback;
  }

  function findingTask(finding: AuditFinding): Task | undefined {
    return finding.current_task || finding.task || finding.current?.task || tasks.find((task) => task.id === finding.task_id);
  }

  function currentVersion(finding: AuditFinding): number | undefined {
    const task = findingTask(finding);
    return finding.current_version
      ?? finding.current?.version
      ?? task?.version
      ?? undefined;
  }

  function mutationVersion(finding: AuditFinding): number {
    // Review versions were added after the append-only snapshot schema. Until
    // every server emits one, the captured version remains the safest guard.
    return finding.version && finding.version > 0 ? finding.version : Math.max(1, finding.captured_version);
  }

  function columnIdentifier(value: unknown): string {
    if (typeof value === 'string') return value;
    if (!value || typeof value !== 'object') return '';
    const record = value as Record<string, unknown>;
    return String(record.id || record.name || record.semantic_state || '');
  }

  function columnLabel(value: unknown): string {
    if (typeof value === 'string') {
      const column = columns.find((item) => item.id === value || item.name === value || item.semantic_state === value);
      return column?.name || stateLabels[value as SemanticState] || value;
    }
    if (!value || typeof value !== 'object') return 'Unknown column';
    const record = value as Record<string, unknown>;
    return String(record.name || stateLabels[record.semantic_state as SemanticState] || record.id || 'Unknown column');
  }

  function currentColumnValue(finding: AuditFinding): unknown {
    const task = findingTask(finding);
    return finding.current_column
      ?? finding.current?.column
      ?? finding.current_column_id
      ?? task?.column_id
      ?? '';
  }

  function changedSinceAudit(finding: AuditFinding): boolean {
    // A backend `true` is authoritative. When the board task is also present
    // in the parent, still compare that live snapshot so an in-session edit
    // cannot be approved during the gap before the next audit refresh.
    if (finding.changed_since_audit === true) return true;
    const version = currentVersion(finding);
    if (version !== undefined && finding.captured_version > 0 && version !== finding.captured_version) return true;
    const source = columnIdentifier(finding.source_column);
    const current = columnIdentifier(currentColumnValue(finding));
    if (!source || !current) return false;
    const sourceColumn = columns.find((column) => column.id === source || column.name === source || column.semantic_state === source);
    const currentColumn = columns.find((column) => column.id === current || column.name === current || column.semantic_state === current);
    if (sourceColumn && currentColumn && sourceColumn.id !== currentColumn.id) return true;
    return false;
  }

  function sourceLabel(finding: AuditFinding): string {
    return columnLabel(finding.source_column);
  }

  function currentLabel(finding: AuditFinding): string {
    const value = currentColumnValue(finding);
    return value ? columnLabel(value) : 'Current column unavailable';
  }

  function destinationState(finding: AuditFinding): SemanticState | '' {
    return semanticStates.includes(finding.proposed_semantic_destination as SemanticState)
      ? finding.proposed_semantic_destination as SemanticState
      : '';
  }

  function destinationLabel(finding: AuditFinding): string {
    const destination = destinationState(finding);
    if (!destination) return 'No move suggested';
    return columns.find((column) => column.semantic_state === destination)?.name || stateLabels[destination];
  }

  function capturedTask(finding: AuditFinding): GithubAuditTask | undefined {
    return captured?.get(finding.task_id);
  }

  function taskLabel(finding: AuditFinding): string {
    const task = findingTask(finding);
    return task?.key || capturedTask(finding)?.key || task?.title || finding.task_id || 'Task';
  }

  function taskTitle(finding: AuditFinding): string {
    return findingTask(finding)?.title || capturedTask(finding)?.title || finding.task_id;
  }

  function confidenceLabel(value: number): string {
    const percent = Math.round(Math.max(0, Math.min(1, Number(value) || 0)) * 100);
    return `${percent}% confidence`;
  }

  function evidenceFor(finding: AuditFinding): string[] {
    return (finding.evidence_refs?.length ? finding.evidence_refs : finding.evidence || []).filter(Boolean);
  }

  function formatDate(value?: string | null): string {
    if (!value) return 'Unknown date';
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return value;
    return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(parsed);
  }

  function ageLabel(audit: AuditRun): string {
    const value = audit.started_at || audit.created_at;
    if (!value) return 'Age unavailable';
    const elapsed = Date.now() - Date.parse(value);
    if (!Number.isFinite(elapsed) || elapsed < 0) return 'Just now';
    const minutes = Math.floor(elapsed / 60000);
    if (minutes < 1) return 'Just now';
    if (minutes < 60) return `${minutes}m old`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours}h old`;
    return `${Math.floor(hours / 24)}d old`;
  }

  function statusClass(status: string | undefined): string {
    const normalized = (status || '').toLowerCase();
    return ['queued', 'running', 'complete', 'partial', 'failed', 'finalized'].includes(normalized) ? normalized : 'unknown';
  }

  function auditReadyForReview(): boolean {
    return Boolean(selectedAudit && ['complete', 'partial'].includes(selectedAudit.status));
  }

  function reviewClass(state: string | undefined): string {
    return ['pending', 'approved', 'dismissed'].includes((state || '').toLowerCase()) ? (state || '').toLowerCase() : 'pending';
  }

  function verdictClass(verdict: string | undefined): string {
    return verdicts.includes(verdict as AuditVerdict) ? verdict || 'needs_attention' : 'needs_attention';
  }

  function findingVersionLabel(finding: AuditFinding): string {
    return finding.captured_version > 0 ? `v${finding.captured_version}` : 'unknown';
  }

  function currentVersionLabel(finding: AuditFinding): string {
    const version = currentVersion(finding);
    return version === undefined ? 'unknown' : `v${version}`;
  }

  function friendlyError(error: unknown, fallback: string): string {
    if (error instanceof ApiError) return error.message;
    if (error instanceof Error) return error.message;
    return fallback;
  }

  function canApply(finding: AuditFinding): boolean {
    const task = findingTask(finding);
    const destination = destinationState(finding);
    const destinationColumn = columns.find((column) => column.semantic_state === destination);
    return auditReadyForReview()
      && finding.review_state === 'approved'
      && finding.verdict === 'move_proposed'
      && !changedSinceAudit(finding)
      && (destination === 'backlog' || destination === 'ready')
      && Boolean(task?.id && task.version && task.column_id && destinationColumn);
  }

  function applyBlockReason(finding: AuditFinding): string {
    if (!auditReadyForReview()) return 'Wait for the agent to finish this audit before review or apply';
    if (changedSinceAudit(finding)) return 'Changed since audit — refresh before applying';
    if (!findingTask(finding)) return 'Current task unavailable';
    if (!columns.find((column) => column.semantic_state === destinationState(finding))) return 'Destination column unavailable';
    if (!['backlog', 'ready'].includes(destinationState(finding))) return 'Use the explicit claim, block, or complete lifecycle action for this destination';
    if (finding.review_state !== 'approved') return 'Approve to preview';
    return '';
  }

  return { canApply, applyBlockReason, normalizeAudit, normalizeFinding, normalizeDetail, unwrapPayload, numberOr, findingTask, capturedTask, taskTitle, currentVersion, mutationVersion, columnIdentifier, columnLabel, currentColumnValue, changedSinceAudit, sourceLabel, currentLabel, destinationState, destinationLabel, taskLabel, confidenceLabel, evidenceFor, formatDate, ageLabel, statusClass, auditReadyForReview, reviewClass, verdictClass, findingVersionLabel, currentVersionLabel, friendlyError };
}

export type AuditHelpers = ReturnType<typeof createAuditHelpers>;
