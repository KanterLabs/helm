import type {
  AssessmentSummary, AuditBaselinePin, AuditFinding, Column, CorrectionResult, GithubAssessment, GithubEvidenceItem
} from '../../../types';
import type { V2Finding } from './types';

// Presentation model for v2 column audits. Everything here derives from the
// server's assessment, summary and correction results; nothing estimates.

export type AuditGroup = 'corrections' | 'correct' | 'unverified';

export const groupLabels: Record<AuditGroup, string> = {
  corrections: 'Corrections',
  correct: 'Already correct',
  unverified: 'Couldn\'t verify'
};

export const isV2 = (finding: AuditFinding): finding is V2Finding =>
  Boolean(finding.assessment && finding.assessment.assessment_version === 2);

export function groupOf(finding: V2Finding): AuditGroup {
  if (finding.assessment.placement === 'correction_recommended') return 'corrections';
  if (finding.assessment.placement === 'column_correct') return 'correct';
  return 'unverified';
}

const plural = (count: number, one: string, many = `${one}s`) => `${count} ${count === 1 ? one : many}`;

/** Headline and balanced counts come from the server summary only. */
export function summaryHeadline(summary: AssessmentSummary): string {
  if (summary.corrections > 0) return `${plural(summary.corrections, 'column correction')} suggested`;
  if (summary.checked > 0 && summary.correct === 0) return 'No placement judgments could be verified';
  return 'No column corrections found';
}

export function summaryLine(summary: AssessmentSummary): string {
  return `${plural(summary.checked, 'card')} checked · ${summary.correct} already correct · ${summary.unable_to_verify} couldn't be verified`;
}

export const shortSha = (sha?: string) => (sha || '').slice(0, 7);

export function baselineLabel(pin: AuditBaselinePin): string {
  const ref = pin.pull_request ? `PR #${pin.pull_request} head` : pin.ref.replace(/^refs\/heads\//, '');
  return `${pin.repository} · ${ref} @ ${shortSha(pin.sha)}`;
}

export function refLabel(pin?: AuditBaselinePin): string {
  if (!pin) return '';
  return pin.pull_request ? `PR #${pin.pull_request} head` : pin.ref.replace(/^refs\/heads\//, '');
}

/** Action-specific language; never a generic Approve. */
export function actionLabel(action: string, targetColumnName = ''): string {
  switch (action) {
    case 'complete': return 'Mark done';
    case 'reopen': return 'Reopen';
    case 'start': return `Move to ${targetColumnName || 'In progress'}`;
    case 'block': return 'Mark blocked';
    case 'move': return `Move to ${targetColumnName || 'column'}`;
    case 'bug_resolve': return 'Resolve as fixed';
    case 'bug_reopen': return 'Reopen bug';
    default: return 'Apply correction';
  }
}

export function columnName(columns: Column[], id: string | null | undefined, fallback = ''): string {
  return columns.find((column) => column.id === id)?.name || fallback || 'Unknown column';
}

export function sourceName(assessment: GithubAssessment, columns: Column[]): string {
  return columnName(columns, assessment.source_column_id, assessment.source_column_name);
}

export function targetName(assessment: GithubAssessment, columns: Column[]): string {
  const fallback = assessment.target_semantic_state === 'completed' ? 'Done' : assessment.target_semantic_state === 'active' ? 'In progress' : '';
  return columnName(columns, assessment.target_column_id, fallback);
}

/** Criterion coverage in words: "2 of 2 conditions supported · 1 contradicted". */
export function conditionTally(assessment: GithubAssessment): string {
  const total = assessment.conditions.length;
  if (!total) return 'No completion conditions captured';
  const count = (result: string) => assessment.conditions.filter((item) => item.result === result).length;
  const parts = [`${count('supported')} of ${plural(total, 'condition')} supported`];
  if (count('contradicted')) parts.push(`${count('contradicted')} contradicted`);
  if (count('unknown')) parts.push(`${count('unknown')} not verified`);
  return parts.join(' · ');
}

export const conditionWords: Record<string, { label: string; icon: string }> = {
  supported: { label: 'Supported', icon: '✓' },
  contradicted: { label: 'Contradicted', icon: '✕' },
  unknown: { label: 'Not verified', icon: '?' }
};

const eligibilityText: Record<string, string> = {
  active_claim: 'Can\'t apply while this card is claimed',
  task_changed: 'Card changed after this audit',
  task_unavailable: 'Card is no longer available',
  checklist_open: 'Finish the required checklist first',
  unmet_dependencies: 'Finish its prerequisites first'
};

/** A publication-time blocker, shown beside the still-visible recommendation. */
export function publicationBlocker(assessment: GithubAssessment): string {
  if (assessment.application_eligibility?.status !== 'blocked') return '';
  const reasons = assessment.application_eligibility.reasons || [];
  return reasons.map((reason) => eligibilityText[reason] || reason.replace(/_/g, ' ')).join(' · ') || 'Can\'t apply right now';
}

export type RowState = 'selectable' | 'blocked' | 'stale' | 'applied' | 'readonly';

export function rowState(finding: V2Finding, receipt?: CorrectionResult): RowState {
  if (receipt && (receipt.outcome === 'applied' || receipt.outcome === 'already_applied')) return 'applied';
  if (groupOf(finding) !== 'corrections') return 'readonly';
  if (finding.changed_since_audit) return 'stale';
  if (publicationBlocker(finding.assessment)) return 'blocked';
  return 'selectable';
}

export const outcomeLabels: Record<string, string> = {
  eligible: 'Ready to apply',
  applied: 'Applied',
  already_applied: 'Already applied',
  skipped: 'Skipped',
  failed: 'Failed',
  unknown: 'Outcome unknown'
};

/** Evidence items cited for one condition, in citation order. */
export function citedItems(ids: string[], items: Map<string, GithubEvidenceItem>): GithubEvidenceItem[] {
  return ids.map((id) => items.get(id)).filter((item): item is GithubEvidenceItem => Boolean(item));
}

export const evidenceClassLabels: Record<string, string> = {
  implementation: 'Implementation',
  test_source: 'Test source',
  observed_check: 'Check result',
  pull_request: 'Pull request',
  task_fact: 'Card fact'
};

/** Render the captured check fact, rather than inferring it from the criterion. */
export function observedCheckOutcome(item: GithubEvidenceItem): string {
  const labels: Record<string, string> = {
    success: 'Passed', failure: 'Failed', error: 'Failed', timed_out: 'Timed out',
    cancelled: 'Canceled', action_required: 'Action required', skipped: 'Skipped',
    neutral: 'Neutral', pending: 'Pending', queued: 'Queued', running: 'Running', in_progress: 'Running'
  };
  try {
    const check = JSON.parse(item.text) as { conclusion?: string; state?: string } | null;
    return labels[check?.conclusion || ''] || labels[check?.state || ''] || 'Unavailable';
  } catch {
    return 'Unavailable';
  }
}

export function lineLabel(item: GithubEvidenceItem): string {
  if (!item.path) return item.title || item.repository;
  if (!item.start_line) return item.path;
  return `${item.path}:${item.start_line}${item.end_line && item.end_line !== item.start_line ? `–${item.end_line}` : ''}`;
}

/** Bounded, numbered lines inserted as text; never HTML. */
export function excerptLines(item: GithubEvidenceItem, max = 24): { n: number; text: string }[] {
  const start = item.start_line || 1;
  return item.text.split('\n').slice(0, max).map((text, index) => ({ n: start + index, text }));
}

/** Linked-PR evidence for a task that is not part of the selected baseline. */
export function prOnlyItems(taskId: string, items: GithubEvidenceItem[]): GithubEvidenceItem[] {
  return items.filter((item) => item.source_role === 'linked_pr' && (!item.task_ids || item.task_ids.includes(taskId)));
}

export function commitUrl(item: GithubEvidenceItem): string {
  if (item.url && /^https:\/\/github\.com\//.test(item.url)) return item.url;
  if (!item.repository || !item.sha || !item.path) return '';
  const lines = item.start_line ? `#L${item.start_line}${item.end_line ? `-L${item.end_line}` : ''}` : '';
  return `https://github.com/${item.repository}/blob/${item.sha}/${item.path}${lines}`;
}
