// Event polling and liveness refreshes for board, my work and the drawer.
import { get as readStore } from 'svelte/store';
import { api } from '../../lib/api';
import { offlineReadOnly } from '../../lib/connectivity';
import { dependencyEventTaskIds } from '../../lib/dependencies';
import { hierarchyEventTaskIds } from '../../lib/hierarchy';
import { reconcileTimelineComments } from '../../lib/timeline';
import type { TimelineCommentReconciliation, TimelineCommentReconciliationResult } from '../../lib/timeline';
import { ApiError } from '../../lib/types';
import type { ActivityEvent } from '../../lib/types';
import { boardEventPageRequiresRefresh } from '../appHelpers';
import { loadBoard } from '../board/load';
import { loadBoardTimeline } from '../boardTimeline';
import { livenessRefreshIntervalMs } from '../constants';
import { refreshDrawerTask } from '../drawer/open';
import { loadDrawerTimeline } from '../drawer/timeline';
import { announce, observeWorkTransitions } from '../feedback';
import { loadIssueMetrics, loadIssues } from '../issues';
import { loadMyWork, loadSidebarCounts, refreshMyWorkTasks } from '../myWork';
import { loadRoadmap } from '../roadmap';
import { board } from '../state/board.svelte';
import { drawer } from '../state/drawer.svelte';
import { live } from '../state/live.svelte';
import { myWork } from '../state/myWork.svelte';
import { session } from '../state/session.svelte';
import { timeline } from '../state/timeline.svelte';
import { workspace } from '../state/workspace.svelte';

export function startPolling() {
  if (live.pollTimer) window.clearInterval(live.pollTimer);
  if (live.pulseTimer) window.clearInterval(live.pulseTimer);
  if (live.livenessRefreshTimer) window.clearInterval(live.livenessRefreshTimer);
  live.pollTimer = window.setInterval(() => void pollEvents(), 15000);
  live.pulseTimer = window.setInterval(() => {
    live.pulseClock = Date.now();
    observeWorkTransitions([...board.tasks, ...myWork.myWorkTasks]);
  }, 30000);
  live.livenessRefreshTimer = window.setInterval(() => void refreshLiveness(), livenessRefreshIntervalMs);
  live.pulseClock = Date.now();
  void pollEvents();
}

export async function refreshLiveness(): Promise<void> {
  if (readStore(offlineReadOnly)) return;
  if (!session.user) return;
  const refreshes: Promise<boolean>[] = [];
  if (workspace.view === 'board' || workspace.view === 'timeline') refreshes.push(refreshBoardTasks());
  if (workspace.view === 'my-work') refreshes.push(refreshMyWorkTasks());
  if (workspace.view === 'audits') workspace.auditRefreshToken += 1;
  if (drawer.drawerTask) refreshes.push(refreshDrawerTask(drawer.drawerTask.id));
  await Promise.all(refreshes).catch(() => undefined);
}

export async function refreshBoardTasks(): Promise<boolean> {
  if (!session.user || (workspace.view !== 'board' && workspace.view !== 'timeline') || !workspace.activeProject || board.boardLoading) return true;
  if (live.boardLivenessInFlight) return live.boardLivenessInFlight;
  const refresh = loadBoard({ background: true });
  live.boardLivenessInFlight = refresh;
  try {
    return await refresh;
  } finally {
    if (live.boardLivenessInFlight === refresh) live.boardLivenessInFlight = null;
  }
}

export async function reconcileDrawerComments(taskId: string, events: ActivityEvent[]): Promise<TimelineCommentReconciliationResult> {
  const loadedCommentTasks = new Map(
    drawer.drawerTimelineItems
      .filter((item) => item.kind === 'comment' && item.comment?.id)
      .map((item) => [item.comment?.id as string, taskId])
  );
  return reconcileTimelineComments(
    events,
    loadedCommentTasks,
    (commentTaskID, commentID) => api.getComment(commentTaskID, commentID),
    (error) => error instanceof ApiError && error.status === 404,
    taskId
  );
}

export async function reconcileBoardComments(projectId: string, events: ActivityEvent[]): Promise<TimelineCommentReconciliationResult> {
  if (timeline.boardTimelineProjectId && timeline.boardTimelineProjectId !== projectId) return { ok: true, reconciliation: {} };
  const loadedCommentTasks = new Map(
    timeline.boardTimelineItems
      .filter((item) => item.kind === 'comment' && item.comment?.id && item.task_id)
      .map((item) => [item.comment?.id as string, item.task_id])
  );
  return reconcileTimelineComments(
    events,
    loadedCommentTasks,
    (commentTaskID, commentID) => api.getComment(commentTaskID, commentID),
    (error) => error instanceof ApiError && error.status === 404
  );
}

export async function pollEvents() {
  if (readStore(offlineReadOnly)) return;
  if (!session.user || live.pollInFlight) return live.pollInFlight || undefined;
  const requestedSession = session.sessionGeneration;
  const requestedCursor = live.eventsCursor;
  let poll: Promise<void>;
  poll = (async () => {
    try {
      const result = await api.listEvents({ after: requestedCursor });
      const isCurrentPoll = () => Boolean(
        session.user
        && session.sessionGeneration === requestedSession
        && live.pollInFlight === poll
      );
      if (!isCurrentPoll()) return;
      // The project timeline has its own newest-first cursor. Refresh its
      // first page on every poll so a quiet or globally backlogged event
      // feed cannot delay visible work from the current board.
      if (workspace.view === 'timeline' && workspace.activeProject?.id) {
        let boardTimelineReconciliation: TimelineCommentReconciliation | undefined;
        let boardTimelineReconciliationSucceeded = true;
        if (result.data.length) {
          const reconciliation = await reconcileBoardComments(workspace.activeProject.id, result.data);
          if (!isCurrentPoll()) return;
          boardTimelineReconciliation = reconciliation.reconciliation;
          boardTimelineReconciliationSucceeded = reconciliation.ok;
        }
        if (
          !(await loadBoardTimeline(workspace.activeProject.id, { reconciliation: boardTimelineReconciliation }))
          || !boardTimelineReconciliationSucceeded
          || !isCurrentPoll()
        ) return;
      }
      if (!result.data.length) return;
      const [issueMetricsReady, sidebarCountsReady] = await Promise.all([
        loadIssueMetrics(),
        loadSidebarCounts()
      ]);
      if (!isCurrentPoll()) return;
      const mergedEvents = new Map<string, ActivityEvent>();
      [...live.events, ...result.data].forEach((event) => mergedEvents.set(event.id || String(event.cursor), event));
      live.events = [...mergedEvents.values()].sort((a, b) => b.cursor - a.cursor).slice(0, 100);
      const nextEventsCursor = Math.max(...result.data.map((event) => event.cursor));
      const currentProjectId = workspace.activeProject?.id;
      const currentView = workspace.view;
      const boardChanged = result.data.some((event) => !event.project_id || event.project_id === currentProjectId);
      const boardRefreshRequired = boardEventPageRequiresRefresh(boardChanged, result.next_cursor);
      const boardTaskIdsBeforePoll = new Set(board.tasks.map((task) => task.id));
      const affectedTaskIds = new Set(result.data.map((event) => event.task_id).filter((id): id is string => Boolean(id)));
      const dependencyAffectedTaskIds = new Set(result.data.flatMap(dependencyEventTaskIds));
      dependencyAffectedTaskIds.forEach((taskId) => affectedTaskIds.add(taskId));
      const hierarchyAffectedTaskIds = new Set(result.data.flatMap(hierarchyEventTaskIds));
      hierarchyAffectedTaskIds.forEach((taskId) => affectedTaskIds.add(taskId));
      let reloadSucceeded = issueMetricsReady && sidebarCountsReady;
      let drawerReconciliation: TimelineCommentReconciliation | undefined;

      if (boardRefreshRequired && (currentView === 'board' || currentView === 'timeline')) {
        reloadSucceeded = (await loadBoard({ background: true })) && reloadSucceeded;
        const missingAffectedTask = [...affectedTaskIds].some((taskId) => boardTaskIdsBeforePoll.has(taskId) && !board.tasks.some((task) => task.id === taskId));
        if (missingAffectedTask) {
          board.boardReconciliationNotice = 'A changed task is outside the loaded board page or current filters. Refresh or load more to find it.';
          announce(board.boardReconciliationNotice);
        }
      }
      if (!isCurrentPoll()) return;
      if (boardChanged && currentView === 'issues') reloadSucceeded = (await loadIssues()) && reloadSucceeded;
      if (!isCurrentPoll()) return;
      if (currentView === 'my-work') reloadSucceeded = (await loadMyWork()) && reloadSucceeded;
      if (!isCurrentPoll()) return;
      if (currentView === 'roadmap') reloadSucceeded = (await loadRoadmap()) && reloadSucceeded;
      if (!isCurrentPoll()) return;

      if (drawer.drawerTask && affectedTaskIds.has(drawer.drawerTask.id)) {
        const drawerTaskId = drawer.drawerTask.id;
        const dependencyChanged = dependencyAffectedTaskIds.has(drawerTaskId);
        const hierarchyChanged = hierarchyAffectedTaskIds.has(drawerTaskId);
        reloadSucceeded = (await refreshDrawerTask(drawerTaskId)) && reloadSucceeded;
        if (dependencyChanged && drawer.drawerTask?.id === drawerTaskId) {
          if (drawer.drawerView === 'details' && drawer.drawerDependencyPanel) {
            reloadSucceeded = (await drawer.drawerDependencyPanel.refreshRelationships()) && reloadSucceeded;
          } else {
            drawer.drawerDependencyRefresh += 1;
          }
        }
        if (hierarchyChanged && drawer.drawerTask?.id === drawerTaskId) {
          if (drawer.drawerView === 'details' && drawer.drawerHierarchyPanel) {
            reloadSucceeded = (await drawer.drawerHierarchyPanel.refreshRelationships()) && reloadSucceeded;
          } else {
            drawer.drawerHierarchyRefresh += 1;
          }
        }
        if (drawer.drawerView === 'activity') {
          const reconciliation = await reconcileDrawerComments(drawerTaskId, result.data);
          if (!isCurrentPoll()) return;
          drawerReconciliation = reconciliation.reconciliation;
          reloadSucceeded = reconciliation.ok && reloadSucceeded;
          reloadSucceeded = (await loadDrawerTimeline(drawerTaskId, { reconciliation: drawerReconciliation })) && reloadSucceeded;
        }
      }
      // Leave the cursor where it was when any dependent read failed. The
      // next poll will replay the event and retry the authoritative refresh.
      if (reloadSucceeded && isCurrentPoll()) live.eventsCursor = nextEventsCursor;
    } catch {
      // Polling is best effort; the visible board remains usable during a blip.
    }
  })().finally(() => {
    // A logout/re-login may have installed a different poll while this
    // response was settling. Only the owner may clear the in-flight slot.
    if (live.pollInFlight === poll) live.pollInFlight = null;
  });
  live.pollInFlight = poll;
  return poll;
}
