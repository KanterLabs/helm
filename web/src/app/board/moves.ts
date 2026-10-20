// Moving and reordering tasks, with undo.
import { api } from '../../lib/api';
import { boardOrderingRefreshReason } from '../../lib/boardOrdering';
import { dependencyMoveExplanation } from '../../lib/dependencies';
import { taskOrderingAnchors } from '../../lib/state';
import { ApiError } from '../../lib/types';
import type { Task } from '../../lib/types';
import { boardMutationReloadCanAnnounce } from '../appHelpers';
import { announce, friendlyError, toast } from '../feedback';
import { board } from '../state/board.svelte';
import { drawer } from '../state/drawer.svelte';
import { workspace } from '../state/workspace.svelte';
import { replaceTask } from '../taskSync';
import { reloadBoardColumns } from './load';
import { boardColumnPageComplete, currentBoardOrderingGate, normalizedOrderingIndex, orderingPlacementLabel, physicalOrderingEnabled, visualOrderingUnavailableMessage } from './ordering';
import { beginBoardMutation, boardMutationIsCurrent } from './pages';

export async function moveTask(task: Task, destinationColumnId: string) {
  if (drawer.taskActionLoading) return;
  if (task.column_id === destinationColumnId) return;
  const destination = board.columns.find((column) => column.id === destinationColumnId);
  const blockedReason = dependencyMoveExplanation(task, destination);
  if (blockedReason) {
    toast('info', blockedReason);
    return;
  }
  const mutationContext = beginBoardMutation();
  if (!mutationContext) return;
  drawer.taskActionLoading = task.id;
  try {
    // Column moves remain append-only and are safe with cursor pagination:
    // the guarded endpoint computes the destination tail under its writer
    // lock, rather than treating the loaded page as the whole column. The
    // legacy move route owns backlog/ready moves; precise last-placement
    // handles active/blocked columns, while completed transitions retain
    // the existing checklist/lifecycle patch path.
    const updated = destination && ['backlog', 'ready'].includes(destination.semantic_state)
      ? await api.moveTask(task.id, {
          destination_column_id: destinationColumnId,
          expected_source_column_id: task.column_id,
          source: 'board'
        }, task.version)
      : destination?.semantic_state === 'completed'
        ? await api.patchTask(task.id, { column_id: destinationColumnId }, task.version)
        : await api.reorderTask(task.id, {
            destination_column_id: destinationColumnId,
            expected_source_column_id: task.column_id,
            source: 'board',
            reason: 'board column move',
            placement: 'last',
            ...(board.columns.find((column) => column.id === task.column_id)?.ordering_version ? { expected_source_ordering_version: board.columns.find((column) => column.id === task.column_id)?.ordering_version } : {}),
            ...(destination?.ordering_version ? { expected_destination_ordering_version: destination.ordering_version } : {})
          }, task.version);
    if (!boardMutationIsCurrent(mutationContext)) return;
    replaceTask(updated, true, false);
    mutationContext.mutationRevision = board.taskMutationRevision;
    const reloadSucceeded = await reloadBoardColumns([task.column_id, destinationColumnId]);
    if (!boardMutationReloadCanAnnounce(boardMutationIsCurrent(mutationContext), reloadSucceeded)) return;
    const destinationName = destination?.name || 'another column';
    toast('success', `${task.key} moved to ${destinationName}.`, {
      label: 'Undo',
      run: () => undoTaskMove(task, updated)
    });
    announce(`${task.key} moved to ${destinationName}.`);
  } catch (error) {
    if (!boardMutationIsCurrent(mutationContext)) return;
    if (error instanceof ApiError && error.status === 409) {
      if (error.details?.current) replaceTask(error.details.current as Task, false, false);
      mutationContext.mutationRevision = board.taskMutationRevision;
      const reloadSucceeded = await reloadBoardColumns([task.column_id, destinationColumnId]);
      if (!boardMutationReloadCanAnnounce(boardMutationIsCurrent(mutationContext), reloadSucceeded)) return;
      announce(`${task.key} changed elsewhere; the board was refreshed.`);
      toast('info', `${task.key} changed elsewhere. The board was refreshed; review the new order.`);
    } else {
      toast('error', friendlyError(error, 'The task could not be moved. Refresh and try again.'));
    }
  } finally {
    if (mutationContext.requestId === board.boardMutationRequest && drawer.taskActionLoading === task.id) drawer.taskActionLoading = '';
  }
}

export async function reorderTaskAt(task: Task, destinationColumnId: string, targetIndex: number, spokenPlacement = '') {
  if (drawer.taskActionLoading) return;
  const destinationOrderingGate = currentBoardOrderingGate(destinationColumnId);
  const sourceOrderingGate = task.column_id === destinationColumnId
    ? destinationOrderingGate
    : currentBoardOrderingGate(task.column_id);
  if (!physicalOrderingEnabled(destinationOrderingGate)) {
    toast('info', visualOrderingUnavailableMessage());
    return;
  }
  const refreshReason = boardOrderingRefreshReason(destinationOrderingGate)
    || boardOrderingRefreshReason(sourceOrderingGate);
  if (refreshReason) {
    toast('info', refreshReason);
    return;
  }
  const destination = board.columns.find((column) => column.id === destinationColumnId);
  const blockedReason = dependencyMoveExplanation(task, destination);
  if (blockedReason) {
    toast('info', blockedReason);
    return;
  }
  const destinationTasks = board.tasksByColumn[destinationColumnId] || [];
  const currentIndex = destinationTasks.findIndex((item) => item.id === task.id);
  const index = normalizedOrderingIndex(task, destinationColumnId, targetIndex);
  if (task.column_id === destinationColumnId && currentIndex >= 0 && currentIndex === index) return;
  let anchors = taskOrderingAnchors(board.visibleTasks, destinationColumnId, index, task.id);
  const visibleDestination = board.visibleTasks.filter((item) => item.column_id === destinationColumnId && item.id !== task.id);
  const completeDestination = board.tasks.filter((item) => item.column_id === destinationColumnId && item.id !== task.id);
  const globalBoundsUnknown = !boardColumnPageComplete(destinationOrderingGate);
  // When a filter hides leading/trailing cards, anchor at the visible edge
  // instead of claiming the global first/last position. If every
  // destination card is hidden, wait for a visible anchor rather than
  // disturbing an unknown part of the column.
  if (globalBoundsUnknown && visibleDestination.length === 0) {
    toast('info', 'Load or reveal a destination card before choosing its exact position.');
    return;
  } else if ((completeDestination.length > visibleDestination.length || globalBoundsUnknown) && anchors.placement === 'first' && visibleDestination[0]) {
    anchors = { before_task_id: visibleDestination[0].id, placement: 'before' };
  } else if ((completeDestination.length > visibleDestination.length || globalBoundsUnknown) && anchors.placement === 'last' && visibleDestination.length) {
    anchors = { after_task_id: visibleDestination[visibleDestination.length - 1].id, placement: 'after' };
  } else if (completeDestination.length > visibleDestination.length && visibleDestination.length === 0) {
    anchors = { placement: 'last' };
  }
  const sourceColumn = board.columns.find((column) => column.id === task.column_id);
  const sourceOrderingVersion = sourceColumn?.ordering_version;
  const destinationOrderingVersion = destination?.ordering_version;
  const mutationContext = beginBoardMutation();
  if (!mutationContext) return;
  drawer.taskActionLoading = task.id;
  try {
    const updated = await api.reorderTask(task.id, {
      destination_column_id: destinationColumnId,
      expected_source_column_id: task.column_id,
      source: 'board',
      reason: 'precise board reorder',
      ...anchors,
      ...(sourceOrderingVersion ? { expected_source_ordering_version: sourceOrderingVersion } : {}),
      ...(destinationOrderingVersion ? { expected_destination_ordering_version: destinationOrderingVersion } : {})
    }, task.version);
    if (!boardMutationIsCurrent(mutationContext)) return;
    replaceTask(updated, true, false);
    mutationContext.mutationRevision = board.taskMutationRevision;
    // Rebalancing updates neighbors without changing their task versions;
    // refresh keeps the filtered board aligned with those authoritative keys.
    const reloadSucceeded = await reloadBoardColumns([task.column_id, destinationColumnId]);
    if (!boardMutationReloadCanAnnounce(boardMutationIsCurrent(mutationContext), reloadSucceeded)) return;
    toast('success', `${task.key} moved to ${board.columns.find((column) => column.id === destinationColumnId)?.name || 'another column'}.`);
    announce(`${task.key} moved to ${spokenPlacement || orderingPlacementLabel(anchors.placement)} position.`);
  } catch (error) {
    if (!boardMutationIsCurrent(mutationContext)) return;
    if (error instanceof ApiError && error.status === 409) {
      if (error.details?.current) replaceTask(error.details.current as Task, false, false);
      mutationContext.mutationRevision = board.taskMutationRevision;
      const reloadSucceeded = await reloadBoardColumns([task.column_id, destinationColumnId]);
      if (!boardMutationReloadCanAnnounce(boardMutationIsCurrent(mutationContext), reloadSucceeded)) return;
      announce(`${task.key} order changed elsewhere; the board was refreshed.`);
      toast('info', `${task.key} changed elsewhere. The board was refreshed; review the new order.`);
    } else {
      toast('error', friendlyError(error, 'The task could not be moved. Refresh and try again.'));
    }
  } finally {
    if (mutationContext.requestId === board.boardMutationRequest && drawer.taskActionLoading === task.id) drawer.taskActionLoading = '';
  }
}

export async function undoTaskMove(previous: Task, moved: Task) {
  if (moved.project_id !== workspace.activeProject?.id || previous.project_id !== workspace.activeProject?.id) return;
  const mutationContext = beginBoardMutation();
  if (!mutationContext) return;
  try {
    const restored = await api.patchTask(
      moved.id,
      { column_id: previous.column_id, position: previous.position },
      moved.version
    );
    if (!boardMutationIsCurrent(mutationContext)) return;
    replaceTask(restored, true, false);
    mutationContext.mutationRevision = board.taskMutationRevision;
    const reloadSucceeded = await reloadBoardColumns([previous.column_id, restored.column_id]);
    if (!boardMutationReloadCanAnnounce(boardMutationIsCurrent(mutationContext), reloadSucceeded)) return;
    announce(`${restored.key} moved back to ${board.columns.find((column) => column.id === previous.column_id)?.name || 'its previous column'}.`);
  } catch (error) {
    if (!boardMutationIsCurrent(mutationContext)) return;
    toast('error', friendlyError(error, 'Undo could not be applied because the task changed elsewhere.'));
  }
}
