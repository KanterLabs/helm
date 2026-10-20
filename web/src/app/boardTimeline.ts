// The board-wide recent-work timeline.
import { api } from '../lib/api';
import { queueBoardTimelineLoad } from '../lib/boardTimeline';
import { mergeTimelineItems } from '../lib/timeline';
import type { TimelineCommentReconciliation } from '../lib/timeline';
import type { TaskTimelineFilter } from '../lib/types';
import { friendlyError } from './feedback';
import { session } from './state/session.svelte';
import { timeline } from './state/timeline.svelte';
import { workspace } from './state/workspace.svelte';

export async function loadBoardTimelinePage(
  projectId = workspace.activeProject?.id,
  options: { older?: boolean; reset?: boolean; reconciliation?: TimelineCommentReconciliation; filter?: TaskTimelineFilter } = {}
): Promise<boolean> {
  const { older = false, reset = false } = options;
  if (!projectId) return true;
  if (older && !timeline.boardTimelineNextCursor) return true;
  if (reset) {
    timeline.boardTimelineItems = [];
    timeline.boardTimelineNextCursor = '';
    timeline.boardTimelineError = '';
  }
  const requestId = ++timeline.boardTimelineRequest;
  const requestedSession = session.sessionGeneration;
  const requestedProjectId = projectId;
  const requestedFilter = options.filter || timeline.boardTimelineFilter;
  const previousCursor = timeline.boardTimelineNextCursor;
  const hadItems = timeline.boardTimelineItems.length > 0;
  if (older) timeline.boardTimelineLoadingOlder = true;
  else timeline.boardTimelineLoading = true;
  timeline.boardTimelineError = '';
  try {
    const result = await api.listProjectTimeline(requestedProjectId, {
      before: older ? previousCursor : undefined,
      limit: 50,
      kind: requestedFilter === 'all' ? undefined : requestedFilter
    });
    if (
      requestId !== timeline.boardTimelineRequest
      || requestedSession !== session.sessionGeneration
      || !session.user
      || workspace.view !== 'timeline'
      || workspace.activeProject?.id !== requestedProjectId
      || timeline.boardTimelineFilter !== requestedFilter
    ) return false;
    const existingItems = older ? timeline.boardTimelineItems : reset ? [] : timeline.boardTimelineItems;
    timeline.boardTimelineItems = mergeTimelineItems(existingItems, result.data || [], options.reconciliation);
    timeline.boardTimelineProjectId = requestedProjectId;
    timeline.boardTimelineNextCursor = older || !hadItems || reset || !previousCursor ? (result.next_cursor || '') : previousCursor;
    return true;
  } catch (error) {
    if (
      requestId === timeline.boardTimelineRequest
      && requestedSession === session.sessionGeneration
      && workspace.view === 'timeline'
      && workspace.activeProject?.id === requestedProjectId
    ) timeline.boardTimelineError = friendlyError(error, 'This board timeline could not be loaded.');
    return false;
  } finally {
    if (older) timeline.boardTimelineLoadingOlder = false;
    else timeline.boardTimelineLoading = false;
  }
}

export function loadBoardTimeline(
  projectId = workspace.activeProject?.id,
  options: { older?: boolean; reset?: boolean; reconciliation?: TimelineCommentReconciliation } = {}
): Promise<boolean> {
  const requestedFilter = timeline.boardTimelineFilter;
  const queued = queueBoardTimelineLoad(
    timeline.boardTimelineLoadQueue,
    () => loadBoardTimelinePage(projectId, { ...options, filter: requestedFilter })
  );
  timeline.boardTimelineLoadQueue = queued.queue;
  return queued.promise;
}

export function setBoardTimelineFilter(next: TaskTimelineFilter) {
  if (!workspace.activeProject || timeline.boardTimelineFilter === next) return;
  timeline.boardTimelineFilter = next;
  void loadBoardTimeline(workspace.activeProject.id, { reset: true });
}

export function loadOlderBoardTimeline() {
  if (workspace.activeProject && timeline.boardTimelineNextCursor) void loadBoardTimeline(workspace.activeProject.id, { older: true });
}
