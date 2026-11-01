import { ApiError, type GithubAuditEvidence, type GithubAuditJob, type GithubEvidenceItem, type Task } from '../../types';

// Server-run GitHub audit presentation. Stages and coverage only reflect what
// the server reported; nothing here estimates progress.

export const JOB_POLL_MS = 2000;

export const jobStages: { id: string; label: string }[] = [
  { id: 'queued', label: 'Queued' },
  { id: 'fetching', label: 'Reading GitHub' },
  { id: 'analyzing', label: 'Analyzing' },
  { id: 'validating', label: 'Checking findings' }
];

const outcomes: Record<string, { label: string; help: string }> = {
  complete: { label: 'Complete', help: 'Every in-scope source was captured. Findings are ready for review.' },
  partial: { label: 'Partial', help: 'Some evidence could not be captured. Findings cover only what was read; missing evidence never proves work is absent or complete.' },
  failed: { label: 'Failed', help: 'This run did not publish findings. Retry starts a fresh capture.' },
  cancelled: { label: 'Cancelled', help: 'Cancelled before publishing. Late results from this attempt are discarded.' },
  interrupted: { label: 'Interrupted', help: 'The server stopped before this run finished. AI work is never repeated automatically; Retry starts a fresh capture.' }
};

export function jobActive(job: GithubAuditJob | null): boolean {
  return Boolean(job && job.stage !== 'finished');
}

export function jobStageLabel(job: GithubAuditJob): string {
  if (job.stage === 'finished') return outcomes[job.outcome]?.label || 'Finished';
  return jobStages.find((stage) => stage.id === job.stage)?.label || job.stage.replace(/_/g, ' ');
}

export function jobOutcomeHelp(job: GithubAuditJob): string {
  return job.stage === 'finished' ? outcomes[job.outcome]?.help || '' : 'Helm is running this audit on the server. You can leave this page; it keeps running.';
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
    plural(job.task_count || 0, 'task', 'tasks'),
    `${plural(job.captured_files || 0, 'file', 'files')} captured`,
    `${job.omitted_files || 0} omitted`
  ];
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
