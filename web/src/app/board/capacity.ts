import { tick } from 'svelte';
import { api } from '../../lib/api';
import { capacityRefusal } from '../../lib/capacity';
import { ApiError } from '../../lib/types';
import type { Task } from '../../lib/types';
import { toast } from '../feedback';
import { board } from '../state/board.svelte';
import { session } from '../state/session.svelte';
import { workspace } from '../state/workspace.svelte';
import { loadBoard } from './load';
import { boardFiltersActive, clearFilters } from './pages';

const capacityRequests = new Map<string, symbol>();

export async function refreshProjectCapacity(projectId: string) {
  if (!session.user || !workspace.projects.some((project) => project.id === projectId)) return;
  const generation = session.sessionGeneration;
  const request = Symbol();
  capacityRequests.set(projectId, request);
  const isCurrent = () => generation === session.sessionGeneration && capacityRequests.get(projectId) === request;
  try {
    // A bulk result applies its returned task snapshots synchronously.
    // Coalesce those changes into one read for the project.
    await Promise.resolve();
    if (!isCurrent()) return;
    const refreshed = await api.getProject(projectId);
    if (!isCurrent()) return;
    const { active_task_count, active_task_limit } = refreshed;
    if (typeof active_task_count !== 'number' || typeof active_task_limit !== 'number') return;
    workspace.projects = workspace.projects.map((project) => project.id === projectId
      ? { ...project, active_task_count, active_task_limit }
      : project);
  } catch {
    // Capacity is advisory in the UI. The write boundary remains authoritative.
  } finally {
    if (capacityRequests.get(projectId) === request) capacityRequests.delete(projectId);
  }
}

export function refreshCapacityAfterTaskChange(task: Task, previous?: Task) {
  if (previous?.column_id === task.column_id) return;
  const stateFor = (columnId: string) => board.columns.find((column) => column.id === columnId)?.semantic_state;
  const before = previous && stateFor(previous.column_id);
  const after = stateFor(task.column_id);
  if (before && after && (before === 'active') === (after === 'active')) return;
  void refreshProjectCapacity(task.project_id);
}

export async function reviewActiveTasks() {
  const projectId = workspace.activeProject?.id;
  const returnFocus = document.activeElement;
  if (boardFiltersActive()) {
    clearFilters();
    await loadBoard();
  }
  await tick();
  if (workspace.activeProject?.id !== projectId || workspace.view !== 'board') return;
  if (document.activeElement !== returnFocus && document.activeElement !== document.body) return;
  const activeColumns = Array.from(document.querySelectorAll<HTMLElement>('[data-active-column]'));
  const target = activeColumns
    .map((column) => column.querySelector<HTMLElement>('[data-task-trigger]'))
    .find(Boolean) || activeColumns[0]?.querySelector<HTMLElement>('h2');
  target?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  target?.focus({ preventScroll: true });
}

export function reportBoardCapacityRefusal(error: unknown, task: Task): boolean {
  if (!(error instanceof ApiError)) return false;
  const message = capacityRefusal(error);
  if (!message) return false;
  void refreshProjectCapacity(task.project_id);
  toast('error', `${task.key} could not be moved. ${message}`, error.code === 'project_capacity_reached'
    ? { label: 'Review In Progress tasks', run: reviewActiveTasks }
    : undefined);
  return true;
}
