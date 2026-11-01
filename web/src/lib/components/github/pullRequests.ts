import type { ArtifactSummary, PullRequestArtifact, PullRequestCheck } from '../../types';

// Pure presentation for GitHub PR links. Nothing here invents metadata: an
// unverified or unavailable artifact only ever shows repository and number.

export type ParsedPullRequest = { repository: string; number: number; canonical: string };

const segment = /^[A-Za-z0-9_.-]+$/;

/** Accepts common GitHub PR URL variants; anything else is refused locally. */
export function parsePullRequestUrl(input: string): ParsedPullRequest | { error: string } {
  const value = input.trim();
  if (!value) return { error: 'Paste a GitHub pull request URL.' };
  let url: URL;
  try {
    url = new URL(/^[a-z][a-z0-9+.-]*:/i.test(value) ? value : `https://${value}`);
  } catch {
    return { error: 'Use a pull request URL like https://github.com/owner/repo/pull/123.' };
  }
  const host = url.hostname.toLowerCase();
  if (!['http:', 'https:'].includes(url.protocol) || (host !== 'github.com' && host !== 'www.github.com')) {
    return { error: 'Only GitHub pull request URLs are supported in this beta.' };
  }
  const [owner, repo, kind, number] = url.pathname.split('/').filter(Boolean);
  if (!owner || !repo || kind !== 'pull' || !/^\d+$/.test(number || '') || !segment.test(owner) || !segment.test(repo)) {
    return { error: 'Use a pull request URL like https://github.com/owner/repo/pull/123.' };
  }
  const parsed = Number(number);
  if (!Number.isSafeInteger(parsed) || parsed < 1) return { error: 'That pull request number is not valid.' };
  const repository = `${owner}/${repo.replace(/\.git$/i, '')}`;
  return { repository, number: parsed, canonical: `https://github.com/${repository}/pull/${parsed}` };
}

export function samePullRequest(artifact: Pick<PullRequestArtifact, 'repository' | 'number'>, parsed: ParsedPullRequest): boolean {
  return artifact.number === parsed.number && artifact.repository.toLowerCase() === parsed.repository.toLowerCase();
}

/** Only absolute https://github.com links are rendered as anchors. */
export function safeGithubUrl(value: string | undefined | null): string {
  if (!value) return '';
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && url.hostname === 'github.com' && !url.username && !url.password ? url.href : '';
  } catch {
    return '';
  }
}

export function shortSha(sha: string | undefined): string {
  return sha ? sha.slice(0, 7) : 'unknown';
}

export type RowTone = 'failing' | 'pending' | 'passing' | 'merged' | 'closed' | 'unknown' | 'unverified' | 'unavailable';

export function artifactStateLabel(artifact: PullRequestArtifact): string {
  if (artifact.verification === 'unavailable') return 'Unavailable';
  const snapshot = artifact.verification === 'verified' ? artifact.snapshot : undefined;
  if (!snapshot) return 'Unverified';
  if (snapshot.state === 'merged') return 'Merged';
  if (snapshot.state === 'closed') return 'Closed';
  return snapshot.draft ? 'Draft' : 'Open';
}

function failed(check: PullRequestCheck): boolean {
  return ['failure', 'timed_out', 'action_required', 'startup_failure', 'error'].includes((check.conclusion || check.state || '').toLowerCase());
}

function pending(check: PullRequestCheck): boolean {
  return !check.conclusion && !['completed', 'success', 'failure', 'error'].includes((check.state || '').toLowerCase());
}

/** Current-head checks summary; incomplete evidence is called out, never hidden. */
export function checksSummary(artifact: PullRequestArtifact): { tone: RowTone; text: string } {
  const snapshot = artifact.snapshot;
  if (!snapshot) return { tone: 'unknown', text: 'Checks unknown' };
  const total = snapshot.checks?.length || 0;
  const failing = (snapshot.checks || []).filter(failed).length;
  const running = (snapshot.checks || []).filter(pending).length;
  switch (snapshot.checks_state) {
    case 'failing': return { tone: 'failing', text: total ? `${failing || 1} of ${total} checks failing` : 'Checks failing' };
    case 'pending': return { tone: 'pending', text: total ? `Checks running · ${total - running} of ${total} done` : 'Checks running' };
    case 'passing': return { tone: 'passing', text: total ? `Checks passed · ${total}` : 'Checks passed' };
    case 'unavailable': return { tone: 'unavailable', text: 'Checks unavailable' };
    default: return { tone: 'unknown', text: total ? 'Checks incomplete' : 'No checks reported' };
  }
}

const reviewWords: Record<string, string> = {
  approved: 'approved',
  changes_requested: 'changes requested',
  commented: 'commented',
  none: 'none yet',
  unknown: 'unknown'
};

export function reviewSummary(artifact: PullRequestArtifact): string {
  const state = artifact.snapshot?.review_state || 'unknown';
  return `Review observation: ${reviewWords[state] || state.replace(/_/g, ' ')}`;
}

/** One plain sentence per state. It never implies the task moved. */
export function artifactExplanation(artifact: PullRequestArtifact, verifying: boolean): string {
  if (verifying) return 'Checking with GitHub…';
  if (artifact.verification === 'unavailable') return 'Helm can’t read this pull request now, so cached details are hidden.';
  const snapshot = artifact.verification === 'verified' ? artifact.snapshot : undefined;
  if (!snapshot) return 'Not verified yet. Helm shows only the repository and number from the link.';
  const checks = snapshot.checks_state;
  if (snapshot.state === 'merged') {
    return checks === 'failing'
      ? 'Merged, but a check on the merged head failed. Merging doesn’t deploy it or complete the task.'
      : 'Merged. That alone doesn’t deploy it or complete the task.';
  }
  if (snapshot.state === 'closed') return 'Closed without merging.';
  if (checks === 'failing') return 'A check is failing on the current head.';
  if (checks === 'pending') return 'Checks are still running. Refresh to re-check.';
  if (checks === 'passing') return 'Checks passed. Merging happens on GitHub, not in Helm.';
  return 'Check results are incomplete or unknown.';
}

const summaryWords: Record<string, string> = {
  failing: 'check failing',
  pending: 'checks running',
  passing: 'checks passed',
  merged: 'merged',
  closed: 'closed',
  unverified: 'not verified',
  unavailable: 'unavailable',
  unknown: 'status unknown'
};

export function summaryTone(summary: ArtifactSummary): RowTone {
  return (summary.state in summaryWords ? summary.state : 'unknown') as RowTone;
}

export function summaryAccessibleLabel(summary: ArtifactSummary, taskKey: string): string {
  const label = summary.label || (summary.count === 1 ? '1 PR' : `${summary.count} PRs`);
  const words = summaryWords[summary.state] || summaryWords.unknown;
  return `${label}, ${words}. Show pull requests for ${taskKey}`;
}

export function relativeTime(value: string | undefined | null, now = Date.now()): string {
  if (!value) return 'never';
  const elapsed = now - Date.parse(value);
  if (!Number.isFinite(elapsed)) return 'at an unknown time';
  const minutes = Math.floor(Math.max(0, elapsed) / 60_000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} d ago`;
}

// Unsent URL drafts survive drawer switches for the lifetime of the page.
const drafts = new Map<string, string>();
export const urlDrafts = {
  get: (taskId: string) => drafts.get(taskId) || '',
  set: (taskId: string, value: string) => { if (value) drafts.set(taskId, value); else drafts.delete(taskId); }
};

// The board badge asks the drawer to land on its Pull requests heading.
let pendingFocus = '';
export function requestPullRequestFocus(taskId: string): void {
  pendingFocus = taskId;
}
export function consumePullRequestFocus(taskId: string): boolean {
  if (pendingFocus !== taskId) return false;
  pendingFocus = '';
  return true;
}
