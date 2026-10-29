// Opening tasks in the drawer from any view and restoring their route origin.
import { api } from '../../lib/api';
import { rememberProject, taskDeepLink } from '../../lib/state';
import { buildTaskShareUrl } from '../../lib/taskShare';
import type { Notification, Task, TaskHierarchyReference, TaskReference, TaskRouteIntent } from '../../lib/types';
import { loadBoard } from '../board/load';
import { applyBoardRoute } from '../board/pages';
import { loadBoardTimeline } from '../boardTimeline';
import { rememberDialogFocus } from '../dialogs';
import { friendlyError, toast } from '../feedback';
import { applyIssueRouteFilters, loadIssues } from '../issues';
import { loadMyWork } from '../myWork';
import { loadProjectReleases, loadReleasesPage } from '../releases/load';
import { loadRoadmap } from '../roadmap';
import { getProjectSlugFromLocation, getTaskRouteFromLocation, isTaskLocation, navigate } from '../routing';
import { applySearchRouteFilters, loadSearch } from '../search';
import { board } from '../state/board.svelte';
import { dialogs } from '../state/dialogs.svelte';
import { drawer } from '../state/drawer.svelte';
import { issues } from '../state/issues.svelte';
import { live } from '../state/live.svelte';
import { myWork } from '../state/myWork.svelte';
import { releasePlan } from '../state/releasePlan.svelte';
import { roadmapData } from '../state/roadmapData.svelte';
import { session } from '../state/session.svelte';
import { timeline } from '../state/timeline.svelte';
import { workspace } from '../state/workspace.svelte';
import { openTaskModal } from '../taskModal';
import { projectForTask } from '../taskQueries';
import { replaceTask } from '../taskSync';
import type { DialogReturnFocus, OpenTaskOptions } from '../types';
import { confirmDrawerTaskSwitch, drawerActionDraftFingerprint, drawerDraftFingerprint, drawerTaskDraftFingerprint, syncCleanDrawerDrafts, syncDraft } from './drafts';
import { loadDrawerTimeline } from './timeline';

export async function openTask(
  task: Task,
  intent: TaskRouteIntent = drawer.taskRouteIntent,
  options: OpenTaskOptions = {}
) {
  if (!options.skipDiscardGuard && !confirmDrawerTaskSwitch(task)) return;
  const route = getTaskRouteFromLocation();
  if (!route) {
    const project = projectForTask(task);
    if (project) {
      drawer.taskRouteOrigin = window.location.pathname + window.location.search;
      navigate(taskDeepLink(project.slug, task.key, intent));
    }
  }
  const requestId = ++drawer.taskDetailRequest;
  rememberDialogFocus(`[data-task-trigger="${CSS.escape(task.id)}"], .work-row`);
  if (options.returnFocus) dialogs.dialogReturnFocus = options.returnFocus;
  drawer.taskRouteIntent = intent;
  drawer.drawerView = intent === 'activity' ? 'activity' : 'details';
  drawer.drawerTask = task;
  drawer.drawerError = '';
  drawer.commentBody = '';
  drawer.drawerTimelineItems = [];
  drawer.drawerTimelineNextCursor = '';
  drawer.drawerTimelineTaskId = task.id;
  drawer.drawerTimelineError = '';
  drawer.drawerTimelineFilter = 'all';
  drawer.drawerTimelineRequest += 1;
  drawer.drawerTimelineLoading = false;
  drawer.drawerTimelineLoadingOlder = false;
  drawer.blockReasonDraft = '';
  drawer.blockReasonOpen = false;
  syncDraft(task);
  drawer.drawerSavedTaskDraftFingerprint = drawerTaskDraftFingerprint();
  drawer.drawerSavedActionDraftFingerprint = drawerActionDraftFingerprint();
  const openingDraft = drawerDraftFingerprint();
  drawer.drawerLoading = true;
  void loadProjectReleases(task.project_id).catch(() => []);
  void loadDrawerTimeline(task.id);
  try {
    const detail = await api.getTask(task.id);
    if (requestId !== drawer.taskDetailRequest || drawer.drawerTask?.id !== task.id) return;
    replaceTask(detail);
    // A fast editor can type before the detail request settles. Hydrate
    // only while every draft field still matches the opening snapshot.
    if (drawerDraftFingerprint() === openingDraft) {
      syncDraft(detail);
      drawer.drawerSavedTaskDraftFingerprint = drawerTaskDraftFingerprint();
      drawer.drawerSavedActionDraftFingerprint = drawerActionDraftFingerprint();
    }
  } catch (error) {
    if (requestId === drawer.taskDetailRequest && drawer.drawerTask?.id === task.id) {
      drawer.drawerError = friendlyError(error, 'Some task details could not be loaded.');
    }
  } finally {
    if (requestId === drawer.taskDetailRequest && drawer.drawerTask?.id === task.id) drawer.drawerLoading = false;
  }
}

export async function refreshDrawerTask(taskId: string): Promise<boolean> {
  if (!drawer.drawerTask || drawer.drawerTask.id !== taskId) return true;
  if (live.drawerLivenessInFlight?.taskId === taskId && live.drawerLivenessInFlight.requestId === live.drawerLivenessRequest) {
    return live.drawerLivenessInFlight.promise;
  }
  const requestId = ++live.drawerLivenessRequest;
  const requestedSession = session.sessionGeneration;
  const refresh = (async () => {
    try {
      const updated = await api.getTask(taskId);
      if (
        live.drawerLivenessRequest !== requestId
        || drawer.drawerTask?.id !== taskId
        || session.sessionGeneration !== requestedSession
        || !session.user
      ) return false;
      replaceTask(updated);
      if (drawer.drawerTask?.id === taskId) syncCleanDrawerDrafts(drawer.drawerTask);
      return true;
    } catch {
      // The next event or the drawer's explicit refresh can retry this read.
      return false;
    }
  })();
  live.drawerLivenessInFlight = { taskId, requestId, promise: refresh };
  try {
    return await refresh;
  } finally {
    if (live.drawerLivenessInFlight?.promise === refresh) live.drawerLivenessInFlight = null;
  }
}

export function handleDependencyTaskUpdated(updated: Task) {
  // Dependency mutations are committed independently from the drawer form.
  // Merge the new version and summary without replacing unsaved draft fields.
  replaceTask(updated, true);
}

export function handleChecklistTaskUpdated(updated: Task) {
  // Checklist mutations are task-versioned just like dependencies. Merge
  // the authoritative task while preserving title/description drafts.
  replaceTask(updated, true);
}

export async function refreshDependencyTask(): Promise<void> {
  if (drawer.drawerTask) await refreshDrawerTask(drawer.drawerTask.id);
}

export function handleHierarchyTaskUpdated(updated: Task) {
  // Parent-edge mutations update the child version and the parent's derived
  // rollup. Merge the returned child without replacing unsaved drawer text.
  replaceTask(updated, true);
}

export async function refreshHierarchyTask(): Promise<void> {
  if (drawer.drawerTask) await refreshDrawerTask(drawer.drawerTask.id);
}

export async function openHierarchyTask(reference: TaskHierarchyReference): Promise<void> {
  const sourceTask = drawer.drawerTask;
  if (!sourceTask) return;
  const project = projectForTask(sourceTask);
  if (!project) {
    drawer.drawerError = 'The linked task project could not be found.';
    return;
  }
  try {
    const related = await api.getTask(reference.id);
    if (related.project_id !== sourceTask.project_id) {
      throw new Error('Hierarchy tasks must belong to the same project.');
    }
    // Check the drawer's dirty state before changing the URL. Navigation is
    // itself observable, so a cancelled discard must leave both the drawer
    // and the current route untouched.
    if (!confirmDrawerTaskSwitch(related)) return;
    if (!drawer.taskRouteOrigin) drawer.taskRouteOrigin = `${window.location.pathname}${window.location.search}`;
    navigate(taskDeepLink(project.slug, related.key, 'details'));
    await openTask(related, 'details', { skipDiscardGuard: true });
  } catch (error) {
    if (drawer.drawerTask?.id === sourceTask.id) {
      drawer.drawerError = friendlyError(error, 'The hierarchy task could not be opened.');
    }
  }
}

export async function createChildTask(): Promise<void> {
  if (!drawer.drawerTask) return;
  await openTaskModal(drawer.drawerTask.id);
}

export async function openDependencyTask(reference: TaskReference): Promise<void> {
  const sourceTask = drawer.drawerTask;
  if (!sourceTask) return;
  const project = projectForTask(sourceTask);
  if (!project) {
    drawer.drawerError = 'The linked task project could not be found.';
    return;
  }
  try {
    const related = await api.getTask(reference.id);
    if (related.project_id !== sourceTask.project_id) {
      throw new Error('Linked tasks must belong to the same project.');
    }
    if (!confirmDrawerTaskSwitch(related)) return;
    if (!drawer.taskRouteOrigin) drawer.taskRouteOrigin = `${window.location.pathname}${window.location.search}`;
    navigate(taskDeepLink(project.slug, related.key, 'details'));
    await openTask(related, 'details', { skipDiscardGuard: true });
  } catch (error) {
    if (drawer.drawerTask?.id === sourceTask.id) {
      drawer.drawerError = friendlyError(error, 'The linked task could not be opened.');
    }
  }
}

export function restoreTaskRouteOrigin(origin: string): Promise<unknown> | void {
  const path = new URL(origin || '/', window.location.origin).pathname;
  if (/^\/my-work\/?$/.test(path)) {
    workspace.view = 'my-work';
    return loadMyWork();
  } else if (/^\/search\/?$/.test(path)) {
    applySearchRouteFilters(new URL(origin, window.location.origin).searchParams);
    workspace.view = 'search';
    return loadSearch();
  } else if (/^\/issues\/?$/.test(path)) {
    applyIssueRouteFilters(new URL(origin, window.location.origin).searchParams);
    workspace.view = 'issues';
    return loadIssues();
  } else if (/^\/roadmap\/?$/.test(path)) {
    workspace.view = 'roadmap';
    return loadRoadmap();
  } else if (/^\/p\/[^/]+\/roadmap\/?$/.test(path)) {
    const slug = path.match(/^\/p\/([^/]+)\/roadmap\/?$/)?.[1];
    const project = workspace.projects.find((item) => item.slug === decodeURIComponent(slug || ''));
    if (project) {
      workspace.activeProjectSlug = project.slug;
      roadmapData.roadmapProjectId = project.id;
    }
    workspace.view = 'roadmap';
    return loadRoadmap();
  } else if (/^\/p\/[^/]+\/(?:focus|releases)\/?$/.test(path)) {
    const releaseSlug = path.match(/^\/p\/([^/]+)\/(?:focus|releases)\/?$/)?.[1];
    const releaseProject = workspace.projects.find((project) => project.slug === decodeURIComponent(releaseSlug || ''));
    if (releaseProject) {
      workspace.activeProjectSlug = releaseProject.slug;
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'releases';
      releasePlan.releaseSelectedId = new URL(origin, window.location.origin).searchParams.get('release') || releasePlan.releaseSelectedId;
      return loadReleasesPage();
    } else {
      workspace.view = 'releases';
    }
  } else if (/^\/p\/[^/]+\/audits(?:\/[^/]+)?\/?$/.test(path)) {
    // A card opened from an audit returns to that same audit and its results.
    workspace.auditIdFromRoute = decodeURIComponent(path.match(/\/audits\/([^/]+)/)?.[1] || '');
    workspace.view = 'audits';
  } else if (/^\/admin\/?$/.test(path)) {
    workspace.view = 'admin';
  } else if (/^\/tickets\/?$/.test(path)) {
    workspace.view = 'tickets';
  } else if (/^\/settings\/?$/.test(path)) {
    workspace.view = 'settings';
  } else if (/^\/p\/[^/]+\/timeline\/?$/.test(path)) {
    // Board timeline task links use the same drawer as board, My Work, and
    // search. Restore the timeline view after closing a task instead of
    // falling through to the default board view.
    const timelineSlug = path.match(/^\/p\/([^/]+)\/timeline\/?$/)?.[1];
    const timelineProject = workspace.projects.find((project) => project.slug === decodeURIComponent(timelineSlug || ''));
    if (timelineProject) {
      workspace.activeProjectSlug = timelineProject.slug;
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'timeline';
      if (timeline.boardTimelineProjectId !== timelineProject.id) return loadBoardTimeline(timelineProject.id, { reset: true });
    } else {
      workspace.view = 'timeline';
    }
  } else {
    workspace.view = 'board';
    const url = new URL(origin || '/', window.location.origin);
    const slug = url.pathname.match(/^\/p\/([^/]+)/)?.[1];
    const project = workspace.projects.find((item) => item.slug === decodeURIComponent(slug || ''));
    if (project) workspace.activeProjectSlug = project.slug;
    applyBoardRoute(url.searchParams);
    return loadBoard();
  }
}

export function drawerTaskShareUrl(task: Task): string {
  const projectSlug = projectForTask(task)?.slug || getProjectSlugFromLocation() || workspace.activeProjectSlug;
  return buildTaskShareUrl(projectSlug, task.key, {
    origin: typeof window !== 'undefined' ? window.location.origin : '',
    intent: drawer.drawerView
  });
}

export async function openNotification(notification: Notification): Promise<void> {
  const requestedSession = session.sessionGeneration;
  const projectId = notification.project_id || '';
  const taskId = notification.task_id || '';
  if (!session.user || !projectId || !taskId) {
    toast('info', 'This notification has no task to open.');
    return;
  }
  const project = workspace.projects.find((item) => item.id === projectId);
  if (!project) {
    toast('error', 'The project for this notification is no longer available.');
    return;
  }
  const knownTask = [...board.tasks, ...myWork.myWorkTasks, ...roadmapData.roadmapLiveTasks, ...issues.issueTasks].find((item) => item.id === taskId);
  try {
    const task = knownTask || await api.getTask(taskId);
    if (requestedSession !== session.sessionGeneration || !session.user || task.project_id !== projectId) {
      if (requestedSession === session.sessionGeneration && session.user) toast('error', 'This notification points to a task that is no longer available.');
      return;
    }
    await openWorkTask(task, null, 'details');
  } catch (error) {
    if (requestedSession === session.sessionGeneration && session.user) toast('error', friendlyError(error, 'This notification points to a task that is no longer available.'));
  }
}

export async function openWorkTask(
  task: Task,
  returnFocus: DialogReturnFocus | null = null,
  intent: TaskRouteIntent = drawer.taskRouteIntent
) {
  const requestedSession = session.sessionGeneration;
  if (!confirmDrawerTaskSwitch(task)) return;
  // Tickets still in the queue open in place instead of on a board.
  const project = projectForTask(task);
  const origin = window.location.pathname + window.location.search;
  drawer.taskRouteOrigin = isTaskLocation() ? (drawer.taskRouteOrigin || origin) : origin;
  if (project && !project.system_kind) {
    workspace.activeProjectSlug = project.slug;
    workspace.recentProjectIds = rememberProject(project.id, localStorage);
    workspace.view = 'board';
    navigate(taskDeepLink(project.slug, task.key, intent));
    await loadBoard();
  } else {
    // A task from a scoped/global search can outlive the project list cache.
    // Keep the drawer usable; the detail response still supplies its task
    // metadata and closeDrawer returns to the originating view.
    // There is no stable project route to push until the project metadata is
    // available, so leave the current URL untouched.
  }
  if (requestedSession !== session.sessionGeneration || !session.user) return;
  await openTask(task, intent, { skipDiscardGuard: true, returnFocus });
}

export async function openBoardTimelineTask(task: Task): Promise<void> {
  const project = workspace.activeProject && workspace.activeProject.id === task.project_id ? workspace.activeProject : projectForTask(task);
  if (!project) return;
  drawer.taskRouteOrigin = `/p/${encodeURIComponent(project.slug)}/timeline`;
  navigate(taskDeepLink(project.slug, task.key, 'activity'));
  await openTask(task, 'activity');
}
