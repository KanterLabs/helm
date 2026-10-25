// Screen-reader announcements, work-state transitions, toasts and friendly errors.
import { agentWorkForTask as agentWorkFor, agentWorkState as workState, isAgentWorkStale as isWorkStale } from '../lib/state';
import { ApiError } from '../lib/types';
import type { Task } from '../lib/types';
import { toastAccessibility } from '../lib/ui';
import { board } from './state/board.svelte';
import { live } from './state/live.svelte';
import { isActionNeeded, showAgentPulse } from './taskQueries';
import type { ToastAction, ToastItem, ToastKind } from './types';

export function workTransitionKey(task: Task): string {
  if (!showAgentPulse(task)) return '';
  const work = agentWorkFor(task);
  if (!work) return '';
  return `${work.state || ''}:${isWorkStale(task)}:${Boolean(work.action_needed)}`;
}

export function observeWorkTransitions(nextTasks: Task[], announceTransitions = true) {
  const next = new Map(live.workTransitionSnapshot);
  nextTasks.forEach((task) => {
    const key = workTransitionKey(task);
    next.set(task.id, key);
    const previous = live.workTransitionSnapshot.get(task.id);
    if (announceTransitions && previous && key && previous !== key) {
      const status = isWorkStale(task) && workState(task) !== 'waiting' ? 'stale' : workState(task) || 'updated';
      announce(`${task.key} is now ${status}${isActionNeeded(task) ? ' · action needed' : ''}.`);
    }
  });
  live.workTransitionSnapshot = next;
}

export function announce(message: string) {
  live.liveAnnouncement = message;
  if (live.announcementTimer) window.clearTimeout(live.announcementTimer);
  live.announcementTimer = window.setTimeout(() => {
    live.liveAnnouncement = '';
  }, 3500);
}

export function clearAnnouncement() {
  live.liveAnnouncement = '';
  if (live.announcementTimer) {
    window.clearTimeout(live.announcementTimer);
    live.announcementTimer = undefined;
  }
  board.boardReconciliationNotice = '';
}

export function toast(kind: ToastKind, message: string, action?: ToastAction) {
  const id = ++live.toastSequence;
  live.toasts = [...live.toasts, { id, kind, message, action, ...toastAccessibility(kind) }];
  window.setTimeout(() => {
    live.toasts = live.toasts.filter((item) => item.id !== id);
  }, 4200);
}

export async function runToastAction(item: ToastItem) {
  if (!item.action || item.action.pending) return;
  item.action.pending = true;
  live.toasts = [...live.toasts];
  try {
    await item.action.run();
  } finally {
    live.toasts = live.toasts.filter((toastItem) => toastItem.id !== item.id);
  }
}

export function friendlyError(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    const focusErrors: Record<string, string> = {
      release_not_found: 'Focus not found',
      release_name_exists: 'A focus with this name already exists in this project',
      release_cross_project: 'This focus belongs to another project',
      release_already_completed: 'This focus is already completed',
      release_not_completed: 'This focus is already active',
      release_has_tasks: 'This focus still has assigned tasks',
      release_incomplete: 'This focus has incomplete required work',
      release_dependency_conflict: 'This focus has a cross-focus dependency conflict',
      release_frozen: 'Completed focus areas cannot accept new task assignments',
      release_queue_changed: 'The focus task list changed; refresh and try again.'
    };
    return focusErrors[error.code] || error.message;
  }
  if (error instanceof Error) return error.message;
  return fallback;
}
