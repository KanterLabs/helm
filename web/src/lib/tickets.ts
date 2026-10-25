// Pure helpers shared by the Tickets workspace and its panels.
import { actorId } from './state';
import { ApiError, type Column, type Task, type TicketQueue, type TicketStatus } from './types';

export const ticketQueues: { value: TicketQueue; label: string }[] = [
  { value: 'open', label: 'All open' },
  { value: 'mine', label: 'My open' },
  { value: 'needs_triage', label: 'Needs triage' },
  { value: 'ready', label: 'Ready' },
  { value: 'in_progress', label: 'In progress' },
  { value: 'waiting', label: 'Waiting' },
  { value: 'completed', label: 'Completed' }
];

export const ticketStatusLabels: Record<TicketStatus, string> = {
  needs_triage: 'Needs triage',
  ready: 'Ready',
  in_progress: 'In progress',
  waiting: 'Waiting',
  completed: 'Completed'
};

export const ticketStatusStates: Record<TicketStatus, Column['semantic_state']> = {
  needs_triage: 'backlog',
  ready: 'ready',
  in_progress: 'active',
  waiting: 'blocked',
  completed: 'completed'
};

export const ticketPriorities: Task['priority'][] = ['urgent', 'high', 'normal', 'low'];
export const ticketPriorityLabels: Record<string, string> = { urgent: 'Urgent', high: 'High', normal: 'Normal', low: 'Low' };

export function validTicketQueue(value: string | null): TicketQueue {
  return ticketQueues.some((item) => item.value === value) ? value as TicketQueue : 'open';
}

export function ticketStatus(task: Task): TicketStatus {
  return task.ticket?.status || 'needs_triage';
}

export function ticketInQueue(task: Task, target: TicketQueue, userId: string): boolean {
  const status = ticketStatus(task);
  if (target === 'open') return status !== 'completed';
  if (target === 'mine') return status !== 'completed' && actorId(task.assignee) === userId;
  return status === target;
}

export function ticketAssigneeLabel(task: Task, userId: string, actorNames: Record<string, string>): string {
  const id = actorId(task.assignee);
  if (!id) return 'Unassigned';
  if (id === userId) return 'You';
  return actorNames[id] || 'Assigned';
}

/** Compact relative age: minutes, then hours up to two days, then days. */
export function ticketAge(value: string | undefined): string {
  const timestamp = Date.parse(value || '');
  if (!Number.isFinite(timestamp)) return '';
  const minutes = Math.max(0, Math.round((Date.now() - timestamp) / 60000));
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}

export function ticketErrorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback;
}

export function isTicketConflict(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 409 || error.status === 412);
}

export type TicketRouteState = { queue: TicketQueue; project: string; q: string; ticket: string };

export function onTicketsRoute(): boolean {
  return /^\/tickets\/?$/.test(window.location.pathname);
}

export function readTicketRoute(): TicketRouteState {
  const params = new URL(window.location.href).searchParams;
  return {
    queue: validTicketQueue(params.get('queue')),
    project: params.get('project') || '',
    q: params.get('q') || '',
    ticket: params.get('ticket') || ''
  };
}

/** Mirror the workspace state into the URL; a push adds a history entry. */
export function writeTicketRoute(state: TicketRouteState, push = false) {
  if (!onTicketsRoute()) return;
  const url = new URL(window.location.href);
  const values: Record<string, string> = { queue: state.queue === 'open' ? '' : state.queue, project: state.project, q: state.q.trim(), ticket: state.ticket };
  for (const [name, value] of Object.entries(values)) {
    if (value) url.searchParams.set(name, value);
    else url.searchParams.delete(name);
  }
  const next = url.pathname + url.search;
  if (next === window.location.pathname + window.location.search) return;
  if (push) window.history.pushState({}, '', next);
  else window.history.replaceState({}, '', next);
}
