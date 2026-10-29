import { ApiError, type GithubAuditEvidence, type GithubAuditJob, type GithubEvidenceItem, type Task } from '../../types';

// Server-run GitHub audit presentation. Stages and coverage only reflect what
// the server reported; nothing here estimates progress.

export const JOB_POLL_MS = 2000;

export const jobStages: { id: string; label: string }[] = [
  { id: 'queued', label: 'Waiting to start' },
  { id: 'fetching', label: 'Reading committed work' },
  { id: 'analyzing', label: 'Checking card criteria' },
  { id: 'validating', label: 'Preparing corrections' }
];

// Finished runs with findings lead with their results; failures never look like
// an empty successful audit, and no card changes without a reviewed apply.
const outcomes: Record<string, { label: string; help: string }> = {
  complete: { label: 'Audit finished', help: 'Results are below. Nothing moves until you review and apply corrections.' },
  partial: { label: 'Audit finished', help: 'Results are below. Unread files unrelated to a card never count against it.' },
  failed: { label: 'Audit failed', help: 'No cards were changed. Retry starts a fresh source capture with the same scope.' },
  cancelled: { label: 'Audit canceled', help: 'No cards were changed. Retry starts a fresh source capture with the same scope.' },
  interrupted: { label: 'Audit interrupted', help: 'No cards were changed. The server stopped before this run finished; AI work is never repeated automatically.' }
};

export function jobActive(job: GithubAuditJob | null): boolean {
  return Boolean(job && job.stage !== 'finished');
}

export function jobStageLabel(job: GithubAuditJob): string {
  if (job.stage === 'finished') return outcomes[job.outcome]?.label || 'Finished';
  return jobStages.find((stage) => stage.id === job.stage)?.label || job.stage.replace(/_/g, ' ');
}

export function jobOutcomeHelp(job: GithubAuditJob): string {
  return job.stage === 'finished' ? outcomes[job.outcome]?.help || '' : 'Leaving this page is safe. The audit keeps running on the server and this link returns to it.';
}

export function jobStepState(job: GithubAuditJob, index: number): 'done' | 'current' | 'upcoming' {
  const current = jobStages.findIndex((stage) => stage.id === job.stage);
  if (current < 0) return 'upcoming';
  if (index < current) return 'done';
  return index === current ? 'current' : 'upcoming';
}

export function canRetry(job: GithubAuditJob | null): boolean {
  return Boolean(job && job.stage === 'finished' && ['failed', 'interrupted', 'cancelled'].includes(job.outcome));
}

const plural = (count: number, one: string, many: string) => `${count} ${count === 1 ? one : many}`;

export function coverageParts(job: GithubAuditJob): string[] {
  return [
    plural(job.task_count || 0, 'card', 'cards'),
    `${plural(job.captured_files || 0, 'file', 'files')} read`,
    `${job.omitted_files || 0} not read`
  ];
}

/** Ended without publishing results; these never present as an audit result. */
export function jobEndedWithoutResults(job: GithubAuditJob | null): boolean {
  return Boolean(job && job.stage === 'finished' && ['failed', 'interrupted', 'cancelled'].includes(job.outcome));
}

/** The authorized existing audit ID returned with a start conflict, if any. */
export function conflictAuditId(error: unknown): string {
  if (!(error instanceof ApiError) || error.status !== 409) return '';
  const details = error.details as Record<string, unknown>;
  const nested = details.audit as { id?: unknown } | undefined;
  const candidate = details.audit_id ?? details.existing_audit_id ?? nested?.id;
  return typeof candidate === 'string' ? candidate : '';
}

export function evidenceIndex(evidence: GithubAuditEvidence | null): Map<string, GithubEvidenceItem> {
  return new Map((evidence?.items || []).map((item) => [item.id, item]));
}

export function evidenceLocation(item: GithubEvidenceItem): string {
  if (!item.path) return item.repository;
  const lines = item.start_line ? `:${item.start_line}${item.end_line && item.end_line !== item.start_line ? `–${item.end_line}` : ''}` : '';
  return `${item.repository} · ${item.path}${lines}`;
}

export const evidenceKindLabels: Record<string, string> = { file: 'File', pull_request: 'Pull request', check: 'Check' };

/**
 * Findings can reference tasks beyond the loaded board page. Fetch only the
 * missing identities, a few at a time, so titles and links stay correct.
 */
export async function loadMissingTasks(ids: string[], getTask: (id: string) => Promise<Task>, limit = 100): Promise<Task[]> {
  const queue = [...new Set(ids)].slice(0, limit);
  const found: Task[] = [];
  const worker = async () => {
    for (let id = queue.shift(); id; id = queue.shift()) {
      try {
        found.push(await getTask(id));
      } catch {
        // Deleted or forbidden tasks keep their captured ID in the finding.
      }
    }
  };
  await Promise.all([worker(), worker(), worker(), worker()]);
  return found;
}
