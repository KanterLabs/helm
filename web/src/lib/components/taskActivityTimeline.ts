// Presentation and draft-bookkeeping helpers for TaskActivityTimeline.svelte.
import type { Comment, TaskTimelineFilter, TaskTimelineItem } from '../types';

export interface CommentDraft {
  comment: Comment;
  body: string;
  originalBody: string;
}

export interface PendingCommentMutation {
  taskId: string;
  commentId: string;
  body: string;
}

export const filterOptions: Array<{ value: TaskTimelineFilter; label: string }> = [
  { value: 'all', label: 'All' },
  { value: 'agent_progress', label: 'Agent' },
  { value: 'comment', label: 'Comments' },
  { value: 'task_change', label: 'Changes' }
];

export function filterLabel(value: TaskTimelineFilter): string {
  return filterOptions.find((option) => option.value === value)?.label || value;
}

export function mutationKey(ownerTaskId: string, commentId: string): string {
  return `${ownerTaskId}\u0000${commentId}`;
}

/** Returns a copy of `map` with `key` set to `value`, or removed when `value` is empty. */
export function withEntry<V>(map: Map<string, V>, key: string, value: V | null | ''): Map<string, V> {
  const next = new Map(map);
  if (value) next.set(key, value);
  else next.delete(key);
  return next;
}

/** Returns task-scoped drafts without one comment's draft, or the same map when nothing changes. */
export function withoutDraft(
  draftsByTask: Map<string, Map<string, CommentDraft>>,
  ownerTaskId: string,
  commentId: string
): Map<string, Map<string, CommentDraft>> {
  if (!ownerTaskId) return draftsByTask;
  const existing = draftsByTask.get(ownerTaskId);
  if (!existing?.has(commentId)) return draftsByTask;
  const nextByTask = new Map(draftsByTask);
  const next = new Map(existing);
  next.delete(commentId);
  if (next.size) nextByTask.set(ownerTaskId, next);
  else nextByTask.delete(ownerTaskId);
  return nextByTask;
}

export function actorName(item: TaskTimelineItem): string {
  return item.actor?.name || item.progress?.actor_id || item.comment?.actor_id || 'Unknown actor';
}

export function actorKind(item: TaskTimelineItem): string {
  return item.actor?.kind === 'agent' ? 'Agent' : item.actor?.kind === 'human' ? 'Human' : 'Actor';
}

export function actorInitial(item: TaskTimelineItem): string {
  return (actorName(item).trim().slice(0, 1) || '?').toUpperCase();
}

export function kindLabel(kind: TaskTimelineItem['kind']): string {
  if (kind === 'agent_progress') return 'Agent update';
  if (kind === 'comment') return 'Comment';
  return 'Task change';
}

export function changeLabel(item: TaskTimelineItem): string {
  const type = item.change?.event_type || 'task.updated';
  const labels: Record<string, string> = {
    'task.created': 'created the task',
    'task.updated': 'updated the task',
    'task.moved': 'moved the task',
    'task.completed': 'completed the task',
    'task.blocked': 'blocked the task',
    'task.claimed': 'claimed the task',
    'task.released': 'released the task',
    'task.renewed': 'renewed the claim',
    'task.claim_renewed': 'renewed the claim',
    'bug.triaged': 'triaged the bug',
    'bug.resolved': 'resolved the bug',
    'bug.reopened': 'reopened the bug',
    'alert.repeated': 'received a repeat alert',
    'comment.updated': 'edited a comment',
    'comment.deleted': 'deleted a comment'
  };
  return labels[type] || type.replace(/[._-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
}

export function changeContext(item: TaskTimelineItem): string {
  const payload = item.change?.payload || {};
  const values = ['summary', 'reason', 'note', 'column', 'column_name', 'state', 'resolution', 'phase']
    .map((key) => payload[key])
    .filter((value): value is string | number => typeof value === 'string' || typeof value === 'number')
    .map(String);
  return values.join(' · ');
}

export function formatRelative(value: string): string {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return 'Unknown time';
  const minutes = Math.round((timestamp - Date.now()) / 60000);
  const absolute = Math.abs(minutes);
  if (absolute < 1) return 'just now';
  if (absolute < 60) return `${absolute}m ${minutes < 0 ? 'ago' : 'from now'}`;
  const hours = Math.round(absolute / 60);
  if (hours < 24) return `${hours}h ${minutes < 0 ? 'ago' : 'from now'}`;
  const days = Math.round(hours / 24);
  return `${days}d ${minutes < 0 ? 'ago' : 'from now'}`;
}

export function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    timeZoneName: 'short'
  }).format(date);
}
