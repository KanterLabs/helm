// Board task selection and bulk changes.
import { api } from '../lib/api';
import type { BulkTaskMutationInput, Task } from '../lib/types';
import { bulkMutationRequestIsCurrent } from './appHelpers';
import { rememberDialogFocus, restoreDialogFocus } from './dialogs';
import { friendlyError, toast } from './feedback';
import { board } from './state/board.svelte';
import { bulk } from './state/bulk.svelte';
import { nav } from './state/nav.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';
import { replaceTask } from './taskSync';

export const selectedTaskSnapshots = new Map<string, Task>();

export function invalidateBulkRequest() {
  bulk.bulkRequest += 1;
  bulk.bulkSubmitting = false;
}

export function closeBulkModal() {
  invalidateBulkRequest();
  if (!bulk.showBulkModal) return;
  bulk.showBulkModal = false;
  restoreDialogFocus();
}

export function retainSelectedTaskSnapshots(ids: ReadonlySet<string>, loadedTasks: readonly Task[]): Task[] {
  // Filters and page reloads can remove a selected task from the rendered
  // board. Keep its last loaded version until deselection, and refresh it
  // whenever it is loaded again. The existing 100-task selection limit
  // bounds this cache; project/session resets clear it through the ID set.
  for (const id of selectedTaskSnapshots.keys()) {
    if (!ids.has(id)) selectedTaskSnapshots.delete(id);
  }
  for (const task of loadedTasks) {
    if (ids.has(task.id)) selectedTaskSnapshots.set(task.id, task);
  }
  return [...ids]
    .map((id) => selectedTaskSnapshots.get(id))
    .filter((task): task is Task => Boolean(task));
}

export function toggleTaskSelection(task: Task) {
  const next = new Set(bulk.selectedTaskIds);
  if (next.has(task.id)) {
    next.delete(task.id);
  } else if (next.size < 100) {
    next.add(task.id);
  } else {
    toast('info', 'Bulk changes are limited to 100 tasks. Clear a selection before adding another.');
    return;
  }
  bulk.selectedTaskIds = next;
}

export function selectVisibleTasks() {
  if (board.boardCriteriaTransition || board.boardLoading || !board.visibleTasks.length) return;
  const next = new Set(bulk.selectedTaskIds);
  const available = Math.max(0, 100 - next.size);
  const unselected = board.visibleTasks.filter((task) => !next.has(task.id));
  unselected.slice(0, available).forEach((task) => next.add(task.id));
  bulk.selectedTaskIds = next;
  if (unselected.length > available) {
    toast('info', 'Only 100 tasks can be selected for one bulk change.');
  }
}

export function clearTaskSelection() {
  bulk.selectedTaskIds = new Set();
  bulk.bulkReviewTasks = [];
  bulk.bulkResult = null;
  bulk.bulkError = '';
}

export function resetBulkForm() {
  bulk.bulkMode = 'partial';
  bulk.bulkOperation = 'priority';
  bulk.bulkPriority = 'normal';
  bulk.bulkAssignee = '';
  bulk.bulkLabels = '';
  bulk.bulkDueDate = '';
  bulk.bulkDestinationColumnId = board.sortedColumns.find((column) => column.semantic_state === 'ready')?.id
    || board.sortedColumns.find((column) => column.semantic_state === 'backlog')?.id
    || board.sortedColumns[0]?.id
    || '';
  bulk.bulkReason = '';
  bulk.bulkError = '';
  bulk.bulkResult = null;
}

export function openBulkModal() {
  if (!bulk.selectedTasks.length || !workspace.activeProject) return;
  rememberDialogFocus('[data-bulk-review-trigger]');
  resetBulkForm();
  // Keep the reviewed task/version set stable while the confirmation dialog
  // is open. Background board refreshes may replace `tasks`, but they must
  // not silently change what the person is about to submit.
  bulk.bulkReviewTasks = bulk.selectedTasks.map((task) => ({ ...task }));
  bulk.showBulkModal = true;
  nav.projectSwitcherOpen = false;
}

export function bulkDueAt(): string | null {
  return bulk.bulkDueDate ? `${bulk.bulkDueDate}T23:59:59Z` : null;
}

export function bulkLabelsInput(): string[] {
  return Array.from(new Set(bulk.bulkLabels.split(',').map((value) => value.trim()).filter(Boolean)));
}

export function buildBulkMutations(reviewTasks: readonly Task[] = bulk.bulkReviewTasks): BulkTaskMutationInput[] {
  return reviewTasks.map((task) => {
    const mutation: BulkTaskMutationInput = {
      task: task.key || task.id,
      version: task.version,
      operation: bulk.bulkOperation
    };
    if (bulk.bulkOperation === 'move') {
      mutation.destination_column_id = bulk.bulkDestinationColumnId;
      mutation.expected_source_column_id = task.column_id;
      mutation.source = 'bulk-ui';
    } else if (bulk.bulkOperation === 'assign') {
      mutation.assignee = bulk.bulkAssignee.trim() || null;
    } else if (bulk.bulkOperation === 'priority') {
      mutation.priority = bulk.bulkPriority;
    } else if (bulk.bulkOperation === 'labels') {
      mutation.labels = bulkLabelsInput();
    } else if (bulk.bulkOperation === 'due_at') {
      mutation.due_at = bulkDueAt();
    } else if (bulk.bulkOperation === 'complete') {
      mutation.comment = bulk.bulkReason.trim() || undefined;
    } else if (bulk.bulkOperation === 'block') {
      mutation.reason = bulk.bulkReason.trim();
    }
    return mutation;
  });
}

export function bulkResultLabel(status: string): string {
  return status === 'applied' ? 'Applied' : status === 'conflict' ? 'Conflict' : 'Skipped';
}

export async function submitBulkChanges() {
  if (!workspace.activeProject || !bulk.bulkReviewTasks.length || bulk.bulkSubmitting) return;
  if (bulk.bulkOperation === 'move' && !bulk.bulkDestinationColumnId) {
    bulk.bulkError = 'Choose a destination column.';
    return;
  }
  if (bulk.bulkOperation === 'block' && !bulk.bulkReason.trim()) {
    bulk.bulkError = 'Add a reason before blocking the selected tasks.';
    return;
  }
  const requested = {
    requestId: ++bulk.bulkRequest,
    sessionGeneration: session.sessionGeneration,
    projectId: workspace.activeProject.id,
    projectSlug: workspace.activeProjectSlug
  };
  const reviewedTasks = bulk.bulkReviewTasks.map((task) => ({ ...task }));
  const input = {
    mode: bulk.bulkMode,
    mutations: buildBulkMutations(reviewedTasks)
  };
  const ownsRequest = () => bulkMutationRequestIsCurrent(requested, {
    requestId: bulk.bulkRequest,
    sessionGeneration: session.sessionGeneration,
    projectId: workspace.activeProject?.id || '',
    projectSlug: workspace.activeProjectSlug,
    authenticated: Boolean(session.user)
  });
  bulk.bulkSubmitting = true;
  bulk.bulkError = '';
  try {
    const result = await api.bulkTasks(requested.projectId, input);
    if (!ownsRequest()) return;
    bulk.bulkResult = result;
    const refreshedReviewTasks = new Map<string, Task>();
    result.results.forEach((item) => {
      if (item.status === 'applied' && item.task) replaceTask(item.task, true);
      const current = item.error?.details?.current;
      if (item.status === 'conflict' && typeof current === 'object' && current !== null && 'id' in current) {
        const currentTask = current as Task;
        // Conflict envelopes carry the authoritative version for human
        // callers. Merge it into the board and the review snapshot so a
        // retry visibly uses the server's current optimistic-concurrency
        // validator instead of silently resubmitting the stale version.
        replaceTask(currentTask);
        refreshedReviewTasks.set(currentTask.id, currentTask);
      }
    });
    const appliedIds = new Set(
      result.results
        .filter((item) => item.status === 'applied' && item.task_id)
        .map((item) => item.task_id as string)
    );
    if (appliedIds.size) {
      bulk.selectedTaskIds = new Set([...bulk.selectedTaskIds].filter((id) => !appliedIds.has(id)));
    }
    bulk.bulkReviewTasks = reviewedTasks
      .filter((task) => !appliedIds.has(task.id))
      .map((task) => refreshedReviewTasks.get(task.id) || task);
    const summary = `${result.applied} applied · ${result.conflicts} conflicts · ${result.skipped} skipped`;
    toast(result.conflicts ? 'info' : 'success', `Bulk changes finished: ${summary}.`);
  } catch (error) {
    if (ownsRequest()) bulk.bulkError = friendlyError(error, 'The bulk change could not be completed.');
  } finally {
    if (ownsRequest()) bulk.bulkSubmitting = false;
  }
}
