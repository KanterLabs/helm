import { ApiError, type CorrectionItemInput, type CorrectionRequest, type CorrectionResult } from '../../../types';

// v2 correction requests and receipts. Confirmation proofs live only in these
// in-memory objects; nothing here logs, stores or exports them.

export interface ItemInputs {
  resolution?: 'fixed' | '';
  reason?: string;
}

export const REASON_LIMIT = 10_000;

/** The exact final request, kept in memory until its outcome is known. */
export interface PendingApply {
  body: CorrectionRequest;
  key: string;
}

function item(findingId: string, action: string, inputs: ItemInputs = {}): CorrectionItemInput {
  const out: CorrectionItemInput = { finding_id: findingId };
  if (action === 'bug_resolve' && inputs.resolution === 'fixed') out.resolution = 'fixed';
  if (action === 'bug_reopen' && inputs.reason?.trim()) out.reason = inputs.reason;
  return out;
}

export function previewRequest(selected: { id: string; action: string }[], inputs: Record<string, ItemInputs>): CorrectionRequest {
  return { assessment_version: 2, preview: true, items: selected.map((entry) => item(entry.id, entry.action, inputs[entry.id])) };
}

/** Final apply: only previewed eligible items, identical inputs, their proofs. */
export function applyRequest(preview: CorrectionRequest, results: CorrectionResult[]): CorrectionRequest {
  const tokens = new Map(results.filter((r) => r.outcome === 'eligible' && r.confirmation_token).map((r) => [r.finding_id, r.confirmation_token as string]));
  return {
    assessment_version: 2,
    preview: false,
    items: preview.items.filter((entry) => tokens.has(entry.finding_id)).map((entry) => ({ ...entry, confirmation_token: tokens.get(entry.finding_id) }))
  };
}

export function inputProblem(action: string, inputs: ItemInputs = {}): string {
  if (action === 'bug_resolve' && inputs.resolution !== 'fixed') return 'Needs a resolution';
  if (action === 'bug_reopen') {
    if (!inputs.reason?.trim()) return 'Needs a reopen reason';
    if (new TextEncoder().encode(inputs.reason).length > REASON_LIMIT) return 'Reason is longer than 10,000 bytes';
  }
  return '';
}

/**
 * A lost or ambiguous response leaves the outcome unknown: the server may have
 * committed. Only a definite client error proves this request changed nothing.
 */
export function transportUnknown(error: unknown): boolean {
  if (!(error instanceof ApiError)) return true;
  return error.status === 0 || error.status === 408 || error.status >= 500;
}

export interface ReceiptCounts {
  applied: number;
  already: number;
  skipped: number;
  failed: number;
  unknown: number;
}

/**
 * A same-key replay returns this request's own committed receipt as
 * `already_applied`; that is still this request's applied result.
 */
export function displayOutcome(result: CorrectionResult, sentKey = ''): string {
  if (!result.outcome_known) return 'unknown';
  if (result.outcome === 'already_applied' && sentKey && result.request_key === sentKey) return 'applied';
  return result.outcome;
}

export function receiptCounts(results: CorrectionResult[], sentKey = ''): ReceiptCounts {
  const counts = { applied: 0, already: 0, skipped: 0, failed: 0, unknown: 0 };
  for (const result of results) {
    const outcome = displayOutcome(result, sentKey);
    if (outcome === 'unknown') counts.unknown += 1;
    else if (outcome === 'applied') counts.applied += 1;
    else if (outcome === 'already_applied') counts.already += 1;
    else if (outcome === 'skipped') counts.skipped += 1;
    else counts.failed += 1;
  }
  return counts;
}

export function receiptHeadline(counts: ReceiptCounts, total: number): string {
  if (counts.unknown) return 'Outcome unknown';
  const changed = counts.applied;
  return `${changed} of ${total} ${total === 1 ? 'correction' : 'corrections'} applied`;
}

export function receiptDetail(counts: ReceiptCounts): string {
  const parts: string[] = [];
  if (counts.already) parts.push(`${counts.already} already applied`);
  if (counts.skipped) parts.push(`${counts.skipped} skipped`);
  if (counts.failed) parts.push(`${counts.failed} failed`);
  if (counts.unknown) parts.push(`${counts.unknown} unknown`);
  return parts.join(' · ');
}

/** Skipped/failed recovery language, only when the server message lacks one. */
export function recoveryHint(result: CorrectionResult): string {
  const message = result.message.toLowerCase();
  if (/re-audit|open the card|restore|refresh|wait for/.test(message)) return '';
  if (message.includes('claimed')) return 'Open the card; wait for its owner to release it.';
  if (message.includes('access')) return 'Restore repository access, then re-audit.';
  if (message.includes('checklist') || message.includes('prerequisite')) return 'Open the card and finish its checklist or prerequisites.';
  if (result.outcome === 'failed') return 'Review the card, then start a new review.';
  return 'Re-audit this card to judge its current facts.';
}
