// My work loading, sidebar counts and live task refreshes.
import { api } from '../lib/api';
import { mergeAuthoritativeTask, mergeAuthoritativeTaskList } from '../lib/liveness';
import { syncCleanDrawerDrafts } from './drawer/drafts';
import { friendlyError, observeWorkTransitions } from './feedback';
import { loadAccessibleReleases } from './releases/load';
import { board } from './state/board.svelte';
import { drawer } from './state/drawer.svelte';
import { live } from './state/live.svelte';
import { myWork } from './state/myWork.svelte';
import { nav } from './state/nav.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';
import { releaseQueryValue } from './taskQueries';
import { mutationsForRequest, myWorkMutationScope } from './taskSync';
import type { MyWorkView } from './types';

export async function loadSidebarCounts(requestedView: MyWorkView = myWork.myWorkView): Promise<boolean> {
  const requestId = ++nav.sidebarCountsRequest;
  const requestedSession = session.sessionGeneration;
  nav.sidebarCountsStatus = 'loading';
  try {
    const result = await api.sidebarCounts({ view: requestedView });
    if (
      requestId !== nav.sidebarCountsRequest
      || session.sessionGeneration !== requestedSession
      || !session.user
      || myWork.myWorkView !== requestedView
    ) return false;
    nav.sidebarCounts = result;
    nav.sidebarCountsStatus = 'known';
    return true;
  } catch {
    if (requestId === nav.sidebarCountsRequest && session.sessionGeneration === requestedSession && session.user && myWork.myWorkView === requestedView) {
      nav.sidebarCountsStatus = 'error';
    }
    return false;
  }
}

export async function loadMyWork(): Promise<boolean> {
  const requestId = ++myWork.myWorkRequest;
  live.myWorkLivenessRequest += 1;
  const requestedSession = session.sessionGeneration;
  const mutationSnapshot = board.taskMutationRevision;
  const requestedView = myWork.myWorkView;
  myWork.myWorkLoading = true;
  myWork.myWorkColumnsLoading = true;
  myWork.myWorkError = '';
  const projectSnapshot = [...workspace.projects];
  try {
    const [workResult, columnResults] = await Promise.all([
      api.allMyWork({ view: requestedView, release_id: releaseQueryValue(myWork.myWorkReleaseFilter) || (myWork.myWorkReleaseFilter === 'unassigned' ? myWork.myWorkReleaseFilter : undefined) }),
      Promise.all(projectSnapshot.map(async (project) => ({
        projectId: project.id,
        columns: (await api.listAllColumns(project.id)).data
      })))
    ]);
    if (
      requestId !== myWork.myWorkRequest
      || myWork.myWorkView !== requestedView
      || session.sessionGeneration !== requestedSession
      || !session.user
    ) return false;
    myWork.myWorkTasks = mergeAuthoritativeTaskList(
      myWork.myWorkTasks,
      workResult.data,
      mutationsForRequest(myWorkMutationScope(requestedView), mutationSnapshot)
    );
    myWork.myWorkColumnsByProject = Object.fromEntries(columnResults.map((result) => [result.projectId, result.columns]));
    void loadAccessibleReleases(projectSnapshot);
    observeWorkTransitions(myWork.myWorkTasks);
    return true;
  } catch (error) {
    if (requestId === myWork.myWorkRequest && session.sessionGeneration === requestedSession && session.user) {
      myWork.myWorkError = friendlyError(error, 'Live work could not be loaded.');
    }
    return false;
  } finally {
    if (requestId === myWork.myWorkRequest && session.sessionGeneration === requestedSession) {
      myWork.myWorkLoading = false;
      myWork.myWorkColumnsLoading = false;
    }
  }
}

export function selectMyWorkView(next: MyWorkView) {
  if (myWork.myWorkView === next) return;
  myWork.myWorkView = next;
  myWork.myWorkFilter = 'all';
  void loadMyWork();
  void loadSidebarCounts(next);
}

export async function refreshMyWorkTasks(): Promise<boolean> {
  if (!session.user || workspace.view !== 'my-work' || myWork.myWorkLoading || myWork.myWorkColumnsLoading) return true;
  if (live.myWorkLivenessInFlight) return live.myWorkLivenessInFlight;

  const requestId = ++live.myWorkLivenessRequest;
  const requestedSession = session.sessionGeneration;
  const mutationSnapshot = board.taskMutationRevision;
  const requestedView = myWork.myWorkView;
  const normalRequestId = myWork.myWorkRequest;
  const refresh = (async () => {
    try {
      const result = await api.allMyWork({ view: requestedView, release_id: releaseQueryValue(myWork.myWorkReleaseFilter) || (myWork.myWorkReleaseFilter === 'unassigned' ? myWork.myWorkReleaseFilter : undefined) });
      if (
        !session.user
        || session.sessionGeneration !== requestedSession
        || workspace.view !== 'my-work'
        || myWork.myWorkView !== requestedView
        || myWork.myWorkRequest !== normalRequestId
        || live.myWorkLivenessRequest !== requestId
      ) return false;
      myWork.myWorkTasks = mergeAuthoritativeTaskList(
        myWork.myWorkTasks,
        result.data,
        mutationsForRequest(myWorkMutationScope(requestedView), mutationSnapshot)
      );
      const updatedDrawer = drawer.drawerTask?.id === undefined
        ? undefined
        : result.data.find((task) => task.id === drawer.drawerTask?.id);
      if (updatedDrawer && drawer.drawerTask?.id === updatedDrawer.id) {
        drawer.drawerTask = mergeAuthoritativeTask(drawer.drawerTask, updatedDrawer);
        syncCleanDrawerDrafts(drawer.drawerTask);
      }
      observeWorkTransitions(myWork.myWorkTasks);
      return true;
    } catch {
      // Liveness refreshes are best effort and must not surface a transient
      // failure as a My Work error or loading state.
      return false;
    }
  })();
  live.myWorkLivenessInFlight = refresh;
  try {
    return await refresh;
  } finally {
    if (live.myWorkLivenessInFlight === refresh) live.myWorkLivenessInFlight = null;
  }
}
