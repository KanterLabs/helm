// The task drawer activity timeline.
import { api } from '../../lib/api';
import { taskDeepLink } from '../../lib/state';
import { mergeTimelineItems } from '../../lib/timeline';
import type { TimelineCommentReconciliation } from '../../lib/timeline';
import type { TaskTimelineFilter, TaskTimelineKind } from '../../lib/types';
import { friendlyError } from '../feedback';
import { getTaskRouteFromLocation, navigate } from '../routing';
import { drawer } from '../state/drawer.svelte';
import { session } from '../state/session.svelte';
import type { DrawerView } from '../types';

export function timelineKindForFilter(filter: TaskTimelineFilter): TaskTimelineKind | undefined {
  return filter === 'all' ? undefined : filter;
}

export async function loadDrawerTimeline(
  taskId = drawer.drawerTask?.id,
  options: { older?: boolean; reconciliation?: TimelineCommentReconciliation } = {}
): Promise<boolean> {
  if (!taskId) return false;
  const older = Boolean(options.older);
  // A poll and an explicit "load older" action may overlap. Deduplicate the
  // same lane and report false so the event poller keeps its cursor and will
  // retry after the in-flight request settles.
  if (drawer.drawerTimelineLoading || drawer.drawerTimelineLoadingOlder) return false;
  const requestId = ++drawer.drawerTimelineRequest;
  const requestedSession = session.sessionGeneration;
  const requestedFilter = drawer.drawerTimelineFilter;
  const requestedTaskId = taskId;
  const previousItems = drawer.drawerTimelineItems;
  const previousCursor = drawer.drawerTimelineNextCursor;
  const hadItems = previousItems.length > 0 && drawer.drawerTimelineTaskId === taskId;
  if (older) drawer.drawerTimelineLoadingOlder = true;
  else drawer.drawerTimelineLoading = true;
  drawer.drawerTimelineTaskId = taskId;
  drawer.drawerTimelineError = '';
  try {
    const result = await api.listTaskTimeline(taskId, {
      before: older ? previousCursor || undefined : undefined,
      limit: 50,
      kind: timelineKindForFilter(requestedFilter)
    });
    if (
      requestId !== drawer.drawerTimelineRequest
      || requestedSession !== session.sessionGeneration
      || drawer.drawerTask?.id !== requestedTaskId
      || drawer.drawerTimelineTaskId !== requestedTaskId
      || drawer.drawerTimelineFilter !== requestedFilter
      || !session.user
    ) return false;
    drawer.drawerTimelineItems = mergeTimelineItems(
      older ? previousItems : hadItems ? previousItems : [],
      result.data || [],
      options.reconciliation
    );
    // A refresh of a list that already includes older pages must retain the
    // existing keyset boundary; the first page's cursor would otherwise
    // replay rows the user has already loaded.
    drawer.drawerTimelineNextCursor = older || !hadItems ? (result.next_cursor || '') : previousCursor;
    return true;
  } catch (error) {
    if (
      requestId === drawer.drawerTimelineRequest
      && requestedSession === session.sessionGeneration
      && drawer.drawerTask?.id === requestedTaskId
      && drawer.drawerTimelineFilter === requestedFilter
    ) drawer.drawerTimelineError = friendlyError(error, 'Task activity could not be loaded.');
    return false;
  } finally {
    if (requestId === drawer.drawerTimelineRequest) {
      if (older) drawer.drawerTimelineLoadingOlder = false;
      else drawer.drawerTimelineLoading = false;
    }
  }
}

export function refreshDrawerTimelineAfterMutation(taskId: string) {
  if (drawer.drawerTask?.id !== taskId) return;
  // A lifecycle mutation creates a timeline event immediately, while the
  // normal event poll can take up to 15 seconds. Invalidate any in-flight
  // read and fetch the newest page so Details → Activity is immediately
  // truthful after claim/block/bug-lifecycle actions.
  drawer.drawerTimelineRequest += 1;
  drawer.drawerTimelineLoading = false;
  drawer.drawerTimelineLoadingOlder = false;
  void loadDrawerTimeline(taskId);
}

export function setDrawerView(next: DrawerView) {
  if (!drawer.drawerTask) return;
  drawer.drawerView = next;
  drawer.taskRouteIntent = next;
  const route = getTaskRouteFromLocation();
  if (route) {
    const path = taskDeepLink(route.projectSlug, route.taskReference, next);
    if (`${window.location.pathname}${window.location.search}` !== path) navigate(path, true);
  }
  if (
    next === 'activity'
    && drawer.drawerTimelineTaskId === drawer.drawerTask.id
    && drawer.drawerTimelineError
  ) void loadDrawerTimeline(drawer.drawerTask.id);
  else if (next === 'activity' && drawer.drawerTimelineTaskId !== drawer.drawerTask.id) void loadDrawerTimeline(drawer.drawerTask.id);
}

export function drawerTabKeydown(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const next: DrawerView = event.key === 'ArrowLeft' || event.key === 'Home' ? 'details' : 'activity';
  setDrawerView(next);
  requestAnimationFrame(() => document.getElementById(`drawer-${next}-tab`)?.focus());
}

export function setDrawerTimelineFilter(next: TaskTimelineFilter) {
  if (!drawer.drawerTask || drawer.drawerTimelineFilter === next) return;
  drawer.drawerTimelineFilter = next;
  drawer.drawerTimelineRequest += 1;
  drawer.drawerTimelineItems = [];
  drawer.drawerTimelineNextCursor = '';
  drawer.drawerTimelineTaskId = drawer.drawerTask.id;
  drawer.drawerTimelineError = '';
  drawer.drawerTimelineLoading = false;
  drawer.drawerTimelineLoadingOlder = false;
  void loadDrawerTimeline(drawer.drawerTask.id);
}

export function loadOlderDrawerActivity() {
  if (drawer.drawerTimelineNextCursor) void loadDrawerTimeline(drawer.drawerTask?.id, { older: true });
}
