// Board card rendering helpers, drag and drop, and quick add.
import { tick } from 'svelte';
import { api } from '../../lib/api';
import type { BoardOrderingGate } from '../../lib/boardOrdering';
import type { BoardCardAction } from '../../lib/components/BoardCardActions.svelte';
import type { Column, Task } from '../../lib/types';
import { plannedReleaseIdForAssignment } from '../appHelpers';
import { ambiguousCreation, creationError, creationOperationKey, hasPendingCreation, reconcileCreation, rememberCreationRoute } from '../creation';
import { isFocusableVisible } from '../dialogs';
import { toast } from '../feedback';
import { board } from '../state/board.svelte';
import { creation } from '../state/creation.svelte';
import { drawer } from '../state/drawer.svelte';
import { releasePlan } from '../state/releasePlan.svelte';
import { session } from '../state/session.svelte';
import { workspace } from '../state/workspace.svelte';
import { recordTaskMutation } from '../taskSync';
import { moveTask, reorderTaskAt } from './moves';
import { adjacentTaskColumn, cardMoveLabel, cardMoveReason, currentBoardOrderingGate, moveTaskBy, moveTaskToPosition, orderingMoveDisabled, orderingMoveLabel, orderingMoveUnavailableReason, physicalOrderingEnabled, visualOrderingUnavailableMessage } from './ordering';
import { syncBoardTaskPages } from './pages';

export function columnColor(column: Column): string {
  return ({ backlog: '#a4aab8', ready: '#4b9cf5', active: '#6d5efc', blocked: '#ec6b75', completed: '#35b88a' } as Record<string, string>)[column.semantic_state] || '#a4aab8';
}

export async function showBoardCardPage(event: MouseEvent, columnId: string, offset: number) {
  const scroller = (event.currentTarget as HTMLElement).closest('.column-cards') as HTMLElement | null;
  board.boardCardOffsets = { ...board.boardCardOffsets, [columnId]: offset };
  await tick();
  scroller?.querySelector<HTMLButtonElement>('[data-task-trigger]')?.focus({ preventScroll: true });
  if (scroller) scroller.scrollTop = 0;
}

export function dragStart(event: DragEvent, task: Task) {
  board.draggingTaskId = task.id;
  board.dragOverColumnId = '';
  event.dataTransfer?.setData('text/plain', task.id);
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
}

export function dragOverColumn(event: DragEvent, columnId: string) {
  event.preventDefault();
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
  if (board.draggingTaskId) board.dragOverColumnId = columnId;
}

export function dragLeaveColumn(event: DragEvent, columnId: string) {
  const target = event.currentTarget as HTMLElement;
  const related = event.relatedTarget as Node | null;
  if (!related || !target.contains(related)) {
    if (board.dragOverColumnId === columnId) board.dragOverColumnId = '';
  }
}

export async function dropTask(event: DragEvent, destinationColumnId: string, boundaryTaskId = '') {
  event.preventDefault();
  event.stopPropagation();
  const taskId = event.dataTransfer?.getData('text/plain') || board.draggingTaskId;
  board.draggingTaskId = '';
  board.dragOverColumnId = '';
  const task = board.tasks.find((item) => item.id === taskId);
  if (!task || drawer.taskActionLoading) return;
  // A drop on the column body is an append-only column move. A drop on a
  // rendered card is a precise reorder and therefore requires the physical
  // position/ascending view; visual sorts cannot be translated safely to
  // physical anchors without loading and sorting the complete column.
  if (!boundaryTaskId) {
    await moveTask(task, destinationColumnId);
    return;
  }
  if (!physicalOrderingEnabled(currentBoardOrderingGate(destinationColumnId))) {
    toast('info', visualOrderingUnavailableMessage());
    return;
  }
  const destinationTasks = board.tasksByColumn[destinationColumnId] || [];
  let targetIndex = destinationTasks.length;
  if (boundaryTaskId) {
    const boundaryIndex = destinationTasks.findIndex((item) => item.id === boundaryTaskId);
    if (boundaryIndex >= 0) {
      const target = event.currentTarget as HTMLElement | null;
      const bounds = target?.getBoundingClientRect();
      targetIndex = boundaryIndex + (bounds && event.clientY >= bounds.top + bounds.height / 2 ? 1 : 0);
    }
  }
  await reorderTaskAt(task, destinationColumnId, targetIndex);
}

export function endDrag() {
  board.draggingTaskId = '';
  board.dragOverColumnId = '';
}

export function boardCardActions(task: Task, ordered: Task[], gate: BoardOrderingGate, busy: boolean): BoardCardAction[] {
  return [
    ...(['first', 'previous', 'next', 'last'] as const).map((placement) => ({
      id: placement, label: orderingMoveLabel(task, placement, gate),
      disabled: orderingMoveDisabled(task, placement, ordered, gate, busy),
      reason: orderingMoveUnavailableReason(placement, gate) || (busy ? 'A move is in progress.' : 'Already at this position.'),
      run: () => moveTaskToPosition(task, placement)
    })),
    ...([-1, 1] as const).map((offset) => ({
      id: offset < 0 ? 'left' : 'right', label: cardMoveLabel(task, offset),
      disabled: !adjacentTaskColumn(task, offset) || Boolean(cardMoveReason(task, offset)) || busy,
      reason: cardMoveReason(task, offset) || (busy ? 'A move is in progress.' : 'No adjacent column.'),
      run: () => moveTaskBy(task, offset)
    }))
  ];
}

export function openQuickAdd(columnId: string, trigger: HTMLButtonElement) {
  if (hasPendingCreation()) return;
  creation.quickAddError = '';
  board.quickAddReturnFocus = trigger;
  board.quickAddColumn = columnId;
  void tick().then(() => {
    if (board.quickAddColumn === columnId) board.quickAddInput?.focus();
  });
}

export function cancelQuickAdd() {
  if (creation.quickAddCreating || creation.quickCreation) return;
  const columnId = board.quickAddColumn;
  const previousTrigger = board.quickAddReturnFocus;
  board.quickAddReturnFocus = null;
  board.quickAddColumn = '';
  void tick().then(() => {
    const fallbackTrigger = Array.from(document.querySelectorAll<HTMLButtonElement>('[data-quick-add-trigger]'))
      .find((trigger) => trigger.dataset.quickAddTrigger === columnId && isFocusableVisible(trigger));
    const target = previousTrigger && isFocusableVisible(previousTrigger) ? previousTrigger : fallbackTrigger;
    target?.focus();
  });
}

export async function submitQuickAdd(columnId: string) {
  if (creation.quickAddCreating) return;
  const title = (board.quickAddTitle[columnId] || '').trim();
  if (!creation.quickCreation && (!title || !workspace.activeProject || !board.columns.some((column) => column.id === columnId))) return;
  if (!creation.quickCreation) {
    rememberCreationRoute();
    creation.quickCreation = {
      projectId: workspace.activeProject!.id,
      key: creationOperationKey(),
      input: { title, column_id: columnId, priority: 'normal', release_id: plannedReleaseIdForAssignment(board.boardReleaseFilter, releasePlan.activeProjectReleases) || null }
    };
  }
  const operation = creation.quickCreation;
  const actor = session.user?.id;
  creation.quickAddCreating = true;
  creation.quickAddError = '';
  drawer.taskActionLoading = `create-${columnId}`;
  try {
    const created = await reconcileCreation(() => api.createTask(operation.projectId, operation.input, operation.key));
    if (actor !== session.user?.id || creation.quickCreation !== operation) return;
    recordTaskMutation(created.id, 'upsert', ['board']);
    if (operation.projectId === workspace.activeProject?.id) {
      board.tasks = [...board.tasks.filter((task) => task.id !== created.id), created];
      syncBoardTaskPages(created);
    }
    board.quickAddTitle = { ...board.quickAddTitle, [columnId]: '' };
    board.quickAddColumn = '';
    board.quickAddReturnFocus = null;
    creation.quickCreation = null;
    toast('success', `${created.key} added to ${board.columns.find((column) => column.id === columnId)?.name || 'the board'}.`);
  } catch (error) {
    if (actor !== session.user?.id || creation.quickCreation !== operation) return;
    if (!ambiguousCreation(error)) creation.quickCreation = null;
    creation.quickAddError = creationError(error, 'The task could not be created.');
  } finally {
    if (actor === session.user?.id) {
      creation.quickAddCreating = false;
      if (drawer.taskActionLoading === `create-${columnId}`) drawer.taskActionLoading = '';
    }
  }
}
