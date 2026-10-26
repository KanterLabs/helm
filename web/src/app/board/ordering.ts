// Ordering availability, labels and keyboard moves for board cards.
import { boardColumnHasKnownGlobalBounds, boardOrderingFiltersActive, boardOrderingRefreshReason, boardOrderingUsesPhysicalOrder, makeBoardOrderingGate } from '../../lib/boardOrdering';
import type { BoardOrderingGate } from '../../lib/boardOrdering';
import { dependencyMoveExplanation } from '../../lib/dependencies';
import type { Column, Task } from '../../lib/types';
import { announce, toast } from '../feedback';
import { board } from '../state/board.svelte';
import { moveTask, reorderTaskAt } from './moves';

export function currentBoardOrderingGate(columnId: string): BoardOrderingGate {
  return makeBoardOrderingGate({
    criteriaTransition: board.boardCriteriaTransition,
    filterTimerPending: board.boardFilterTimer !== undefined,
    boardLoading: board.boardLoading,
    metadataError: Boolean(board.boardError),
    pageRefreshActive: Boolean(board.boardColumnRefreshes[columnId]),
    page: board.boardPages[columnId],
    filters: board.filters,
    workFilter: board.boardWorkFilter,
    sort: board.boardSort,
    order: board.boardOrder
  });
}

export function physicalOrderingEnabled(gate: BoardOrderingGate): boolean {
  return boardOrderingUsesPhysicalOrder(gate);
}

export function boardColumnPageComplete(gate: BoardOrderingGate): boolean {
  return boardColumnHasKnownGlobalBounds(boardOrderingFiltersActive(gate), gate.page, {
    metadataLoading: gate.boardLoading || gate.metadataError,
    pageRefreshActive: gate.pageRefreshActive,
    criteriaTransition: gate.criteriaTransition || gate.filterTimerPending
  });
}

export function globalOrderingUnavailableMessage(gate: BoardOrderingGate): string {
  if (boardOrderingFiltersActive(gate)) {
    return 'Home and End cannot move to a global first or last position while board filters are active. Clear filters; previous and next remain available for visible cards.';
  }
  if (gate.criteriaTransition || gate.filterTimerPending) {
    return 'Home and End cannot move to a global first or last position while the board criteria are changing. Wait for the refreshed column; previous and next remain available for visible cards.';
  }
  if (gate.metadataError) {
    return 'Home and End cannot move to a global first or last position because the board metadata refresh failed. Retry the board; previous and next remain available for loaded cards.';
  }
  if (gate.boardLoading || gate.pageRefreshActive || gate.page?.loading) {
    return 'Home and End cannot move to a global first or last position while this board column is refreshing. Wait for the refreshed page; previous and next remain available for visible cards.';
  }
  if (gate.page?.error) {
    return 'Home and End cannot move to a global first or last position because this column did not finish loading. Retry the column; previous and next remain available for loaded cards.';
  }
  if (gate.page?.nextCursor) {
    return 'Home and End cannot move to a global first or last position until this column is fully loaded. Load more tasks; previous and next remain available for loaded cards.';
  }
  return 'Home and End cannot move to a global first or last position until this column is fully loaded.';
}

export function announceGlobalOrderingUnavailable(gate: BoardOrderingGate) {
  const message = globalOrderingUnavailableMessage(gate);
  announce(message);
  toast('info', message);
}

export function visualOrderingUnavailableMessage(): string {
  return 'Precise ordering is unavailable unless Sort tasks is Board order and Sort direction is Ascending.';
}

export function orderingMoveUnavailableReason(
  placement: 'first' | 'previous' | 'next' | 'last',
  gate: BoardOrderingGate
): string {
  if (!physicalOrderingEnabled(gate)) return visualOrderingUnavailableMessage();
  const refreshReason = boardOrderingRefreshReason(gate);
  if (refreshReason) return refreshReason;
  if ((placement === 'first' || placement === 'last') && !boardColumnPageComplete(gate)) {
    return globalOrderingUnavailableMessage(gate);
  }
  return '';
}

export function orderingMoveDisabled(
  task: Task,
  placement: 'first' | 'previous' | 'next' | 'last',
  ordered: Task[],
  gate: BoardOrderingGate,
  taskBusy: boolean
): boolean {
  if (!physicalOrderingEnabled(gate) || taskBusy) return true;
  const index = ordered.findIndex((item) => item.id === task.id);
  if (index < 0) return true;
  if (orderingMoveUnavailableReason(placement, gate)) return true;
  return placement === 'first' || placement === 'previous'
    ? index === 0
    : index === ordered.length - 1;
}

export function orderingMoveTitle(
  placement: 'first' | 'previous' | 'next' | 'last',
  gate: BoardOrderingGate
): string {
  return orderingMoveUnavailableReason(placement, gate) || `Move to ${placement} position`;
}

export function normalizedOrderingIndex(task: Task, destinationColumnId: string, targetIndex: number): number {
  const destinationTasks = board.tasksByColumn[destinationColumnId] || [];
  const index = Math.max(0, Math.min(destinationTasks.length, Math.trunc(targetIndex)));
  const currentIndex = destinationTasks.findIndex((item) => item.id === task.id);
  return task.column_id === destinationColumnId && currentIndex >= 0 && currentIndex < index ? index - 1 : index;
}

export function keyboardMove(event: KeyboardEvent, task: Task) {
  if (event.altKey && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
    event.preventDefault();
    const destination = adjacentTaskColumn(task, event.key === 'ArrowLeft' ? -1 : 1);
    if (destination) void moveTask(task, destination.id);
    return;
  }
  if (!event.altKey || !['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
  const ordered = board.tasksByColumn[task.column_id] || [];
  const currentIndex = ordered.findIndex((item) => item.id === task.id);
  if (currentIndex < 0) return;
  const orderingGate = currentBoardOrderingGate(task.column_id);
  if (!physicalOrderingEnabled(orderingGate)) {
    event.preventDefault();
    const message = visualOrderingUnavailableMessage();
    announce(message);
    toast('info', message);
    return;
  }
  const refreshReason = boardOrderingRefreshReason(orderingGate);
  if (refreshReason) {
    event.preventDefault();
    announce(refreshReason);
    toast('info', refreshReason);
    return;
  }
  if ((event.key === 'Home' || event.key === 'End') && !boardColumnPageComplete(orderingGate)) {
    event.preventDefault();
    announceGlobalOrderingUnavailable(orderingGate);
    return;
  }
  const targetIndex = event.key === 'Home' ? 0 : event.key === 'End' ? ordered.length : event.key === 'ArrowUp' ? currentIndex - 1 : currentIndex + 2;
  if (targetIndex < 0 || targetIndex > ordered.length || (event.key === 'ArrowUp' && currentIndex === 0) || (event.key === 'ArrowDown' && currentIndex === ordered.length - 1)) return;
  event.preventDefault();
  const spokenPlacement = event.key === 'Home' ? 'first' : event.key === 'End' ? 'last' : event.key === 'ArrowUp' ? 'previous' : 'next';
  void reorderTaskAt(task, task.column_id, targetIndex, spokenPlacement);
}

export function adjacentTaskColumn(task: Task, offset: -1 | 1): Column | undefined {
  const index = board.sortedColumns.findIndex((column) => column.id === task.column_id);
  return board.sortedColumns[index + offset];
}

export function cardMoveReason(task: Task, offset: -1 | 1): string {
  return dependencyMoveExplanation(task, adjacentTaskColumn(task, offset));
}

export function cardMoveLabel(task: Task, offset: -1 | 1): string {
  const direction = offset < 0 ? 'previous' : 'next';
  const reason = cardMoveReason(task, offset);
  return `Move ${task.key} to ${direction} column${reason ? `. Unavailable: ${reason}` : ''}`;
}

export async function moveTaskBy(task: Task, offset: -1 | 1): Promise<void> {
  const destination = adjacentTaskColumn(task, offset);
  if (destination) await moveTask(task, destination.id);
}

export function orderingPlacementLabel(placement: string): string {
  if (placement === 'first') return 'first';
  if (placement === 'last') return 'last';
  return 'the selected';
}

export function orderingMoveLabel(
  task: Task,
  placement: 'first' | 'previous' | 'next' | 'last',
  gate: BoardOrderingGate
): string {
  const reason = orderingMoveUnavailableReason(placement, gate);
  return `Move ${task.key} to ${placement} position${reason ? `. Unavailable: ${reason}` : ''}`;
}

export async function moveTaskToPosition(task: Task, placement: 'first' | 'previous' | 'next' | 'last'): Promise<void> {
  const ordered = board.tasksByColumn[task.column_id] || [];
  const index = ordered.findIndex((item) => item.id === task.id);
  if (index < 0) return;
  const targetIndex = placement === 'first' ? 0 : placement === 'last' ? ordered.length : placement === 'previous' ? index - 1 : index + 2;
  await reorderTaskAt(task, task.column_id, targetIndex, placement);
}
