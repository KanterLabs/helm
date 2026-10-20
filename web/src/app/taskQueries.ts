// Pure-ish task predicates and labels: releases, work buckets, claims and due dates.
import { actorId, actorName, agentWorkActionNeeded, agentWorkForTask as agentWorkFor, agentWorkUpdatedAt as workUpdatedAt, isDueSoon, isMissingAgentWorkCandidate, isOverdue, matchesAgentWorkFilter, shouldShowAgentPulse } from '../lib/state';
import type { Project, Task } from '../lib/types';
import { claimWarningThresholdMs } from './constants';
import { board } from './state/board.svelte';
import { live } from './state/live.svelte';
import { myWork } from './state/myWork.svelte';
import { releasePlan } from './state/releasePlan.svelte';
import { session } from './state/session.svelte';
import { settings } from './state/settings.svelte';
import { workspace } from './state/workspace.svelte';
import type { MyWorkRow, ReleaseModel, TaskWithRelease, WorkFilter } from './types';

export function isActionNeeded(task: Task, now = live.pulseClock): boolean {
  return agentWorkActionNeeded(task, now, semanticStateForTask(task));
}

export function taskWithRelease(task: Task | null | undefined): TaskWithRelease | null {
  return task ? task as TaskWithRelease : null;
}

export function taskReleaseId(task: Task | null | undefined): string {
  const value = taskWithRelease(task);
  return value?.release_id || value?.release?.id || '';
}

export function taskReleaseName(task: Task | null | undefined): string {
  const value = taskWithRelease(task);
  return value?.release?.name
    || releasePlan.allReleaseOptions.find((release) => release.id === taskReleaseId(task))?.name
    || releasePlan.releases.find((release) => release.id === taskReleaseId(task))?.name
    || '';
}

export function releaseQueryValue(value: string): string | undefined {
  return value !== 'all' ? value : undefined;
}

export function taskMatchesReleaseFilter(task: Task | null | undefined, value: string): boolean {
  if (value === 'all') return true;
  const releaseId = taskReleaseId(task);
  if (value === 'unassigned') return !releaseId;
  return releaseId === value;
}

export function releaseOptionLabel(release: ReleaseModel): string {
  const project = workspace.projects.find((item) => item.id === release.project_id);
  return project ? `${project.key} · ${release.name}` : release.name;
}

export function matchesWorkFilter(task: Task, filter: WorkFilter, now = live.pulseClock): boolean {
  return matchesAgentWorkFilter(task, filter, now, semanticStateForTask(task));
}

export function sortWorkRows(rows: MyWorkRow[]): MyWorkRow[] {
  return [...rows].sort((a, b) => {
    const aTime = Date.parse(workUpdatedAt(a.task) || (myWork.myWorkView === 'assigned' ? a.task.updated_at || '' : '')) || 0;
    const bTime = Date.parse(workUpdatedAt(b.task) || (myWork.myWorkView === 'assigned' ? b.task.updated_at || '' : '')) || 0;
    return bTime - aTime || a.task.number - b.task.number;
  });
}

export function agentLabelForTask(task: Task): string {
  const work = agentWorkFor(task);
  const actor = work?.actor_id;
  if (actor) return settings.agents.find((agent) => agent.id === actor)?.name || actor;
  return actorName(task.claimed_by) || actorId(task.claimed_by);
}

export function semanticStateForTask(task: Task, localColumns = board.columns): string {
  return localColumns.find((column) => column.id === task.column_id)?.semantic_state || '';
}

export function isMissingPulseCandidate(task: Task, localColumns = board.columns): boolean {
  return isMissingAgentWorkCandidate(task, semanticStateForTask(task, localColumns));
}

export function showAgentPulse(task: Task, localColumns = board.columns): boolean {
  return shouldShowAgentPulse(task, semanticStateForTask(task, localColumns));
}

export function claimIsActive(task: Task, now = live.pulseClock): boolean {
  if (!task.claimed_by || !task.claim_expires_at) return Boolean(task.claimed_by);
  const expires = Date.parse(task.claim_expires_at);
  return Number.isNaN(expires) || expires > now;
}

export function claimCountdown(task: Task, now = live.pulseClock): string {
  if (!task.claim_expires_at) return '';
  const expires = Date.parse(task.claim_expires_at);
  if (Number.isNaN(expires)) return '';
  const difference = expires - now;
  if (difference <= 0) return 'expired';
  const minutes = Math.ceil(difference / 60000);
  if (minutes < 60) return `${minutes}m left`;
  const hours = Math.floor(minutes / 60);
  const remainder = minutes % 60;
  if (hours < 24) return `${hours}h${remainder ? ` ${remainder}m` : ''} left`;
  return `${Math.floor(hours / 24)}d${hours % 24 ? ` ${hours % 24}h` : ''} left`;
}

export function claimExpiresSoon(task: Task, now = live.pulseClock): boolean {
  if (!task.claim_expires_at || !claimIsActive(task, now)) return false;
  const expires = Date.parse(task.claim_expires_at);
  return Number.isFinite(expires) && expires - now <= claimWarningThresholdMs;
}

export function claimExpiryExact(value?: string): string {
  if (!value) return '';
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    timeZoneName: 'short'
  }).format(parsed);
}

export function projectForTask(task: Task): Project | undefined {
  return workspace.projects.find((project) => project.id === task.project_id);
}

export function taskDueClass(task: Task): string {
  if (isOverdue(task.due_at)) return 'overdue';
  if (isDueSoon(task.due_at)) return 'due-soon';
  return '';
}

export function claimAction(task: Task | null, now = live.pulseClock): 'claim' | 'renew' {
  return task?.claimed_by && actorId(task.claimed_by) === session.user?.id && claimIsActive(task, now) ? 'renew' : 'claim';
}

export function claimConflict(task: Task | null, now = live.pulseClock): boolean {
  return Boolean(task?.claimed_by && actorId(task.claimed_by) !== session.user?.id && claimIsActive(task, now));
}

export function claimOwnerLabel(task: Task | null): string {
  if (!task?.claimed_by) return '';
  return actorName(task.claimed_by) || actorId(task.claimed_by) || 'another actor';
}
