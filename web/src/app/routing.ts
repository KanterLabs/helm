// URL routing, project selection and view switching.
import { api } from '../lib/api';
import { helmStorageKeys, legacyRoadmapStorageKeys, parseTaskRoute, readMigratedStorage, rememberProject, taskDeepLink } from '../lib/state';
import type { Project } from '../lib/types';
import { loadProjectAdmin } from './admin';
import { loadBoard } from './board/load';
import { applyBoardRoute, invalidateBoardColumnRequests, syncBoardViewURL } from './board/pages';
import { loadBoardTimeline } from './boardTimeline';
import { invalidateBulkRequest } from './bulk';
import { hasPendingCreation } from './creation';
import { closeDrawer, confirmDrawerDiscard } from './drawer/drafts';
import { openTask } from './drawer/open';
import { clearAnnouncement } from './feedback';
import { applyIssueRouteFilters, loadIssues, syncIssueViewURL } from './issues';
import { loadMyWork } from './myWork';
import { closeNavMenus } from './nav/sidebar';
import { closeCommandPalette } from './palette';
import { loadReleasesPage } from './releases/load';
import { loadRoadmap } from './roadmap';
import { applySearchRouteFilters, loadSearch, searchURL } from './search';
import { loadAgents, loadCodexAccount } from './settings';
import { board } from './state/board.svelte';
import { bulk } from './state/bulk.svelte';
import { creation } from './state/creation.svelte';
import { dialogs } from './state/dialogs.svelte';
import { drawer } from './state/drawer.svelte';
import { issues } from './state/issues.svelte';
import { myWork } from './state/myWork.svelte';
import { nav } from './state/nav.svelte';
import { releasePlan } from './state/releasePlan.svelte';
import { roadmapData } from './state/roadmapData.svelte';
import { timeline } from './state/timeline.svelte';
import { workspace } from './state/workspace.svelte';
import { projectForTask } from './taskQueries';
import type { View } from './types';

export function getProjectSlugFromLocation(): string {
  const match = window.location.pathname.match(/^\/p\/([^/]+)/);
  return match ? decodeURIComponent(match[1]) : '';
}

export function getTaskRouteFromLocation() {
  return parseTaskRoute(window.location.pathname, window.location.search, window.location.hash);
}

export function isTaskLocation(): boolean {
  return Boolean(getTaskRouteFromLocation());
}

export function isProjectRoadmapLocation(): boolean {
  return /^\/p\/[^/]+\/roadmap\/?$/.test(window.location.pathname);
}

export function isProjectFocusLocation(): boolean {
  return /^\/p\/[^/]+\/focus\/?$/.test(window.location.pathname);
}

export function isProjectReleasesLocation(): boolean {
  return isProjectFocusLocation() || /^\/p\/[^/]+\/releases\/?$/.test(window.location.pathname);
}

export function canonicalizeProjectFocusLocation(): void {
  if (typeof window === 'undefined' || !/^\/p\/[^/]+\/releases\/?$/.test(window.location.pathname)) return;
  const pathname = window.location.pathname.replace(/\/releases\/?$/, '/focus');
  const target = `${pathname}${window.location.search}${window.location.hash}`;
  const current = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  if (current !== target) window.history.replaceState({}, '', target);
}

export function isProjectTimelineLocation(): boolean {
  return /^\/p\/[^/]+\/timeline\/?$/.test(window.location.pathname);
}

export function isProjectAuditLocation(): boolean {
  return /^\/p\/[^/]+\/audits(?:\/[^/]+)?\/?$/.test(window.location.pathname);
}

export function getAuditIdFromLocation(): string {
  const match = window.location.pathname.match(/^\/p\/[^/]+\/audits\/([^/]+)\/?$/);
  return match ? decodeURIComponent(match[1]) : '';
}

export async function openTaskFromRoute(
  route: NonNullable<ReturnType<typeof parseTaskRoute>>,
  routeProject = workspace.projects.find((project) => project.slug === route.projectSlug)
): Promise<void> {
  if (!routeProject) return;
  const requestedLocation = window.location.pathname + window.location.search;
  const candidate = [...board.tasks, ...myWork.myWorkTasks, ...roadmapData.roadmapLiveTasks, ...issues.issueTasks]
    .find((task) => task.project_id === routeProject.id && (task.key === route.taskReference || task.id === route.taskReference || String(task.number) === route.taskReference));
  let task = candidate;
  if (!task) {
    try {
      const loaded = await api.getTask(route.taskReference);
      if (loaded.project_id === routeProject.id) task = loaded;
    } catch {
      // The route remains stable even if a deleted or inaccessible task is
      // opened; the board error gives the person an actionable explanation.
    }
  }
  if (window.location.pathname + window.location.search !== requestedLocation) return;
  if (!task) {
    board.boardError = `Task ${route.taskReference} could not be found in ${routeProject.name}.`;
    return;
  }
  drawer.taskRouteIntent = route.intent;
  const retainedOrigin = window.history.state?.helmTaskOrigin;
  drawer.taskRouteOrigin = typeof retainedOrigin === 'string' && retainedOrigin.startsWith('/') && !retainedOrigin.startsWith('//')
    ? retainedOrigin : `/p/${encodeURIComponent(routeProject.slug)}`;
  await openTask(task, route.intent);
}

export function navigate(path: string, replace = false) {
  if (`${window.location.pathname}${window.location.search}` !== path) {
    dialogs.dialogFocusRestoreRequest += 1;
    const state = /\/tasks\//.test(path) && drawer.taskRouteOrigin ? { helmTaskOrigin: drawer.taskRouteOrigin } : {};
    window.history[replace ? 'replaceState' : 'pushState'](state, '', path);
  }
}

export async function selectProject(project: Project, push = true, routeReleaseFilter?: string) {
  if (hasPendingCreation()) return;
  workspace.projectSwitchVersion += 1;
  timeline.boardTimelineRequest += 1;
  if (drawer.drawerTask && !closeDrawer()) return;
  clearAnnouncement();
  board.boardMutationRequest += 1;
  drawer.taskActionLoading = '';
  workspace.activeProjectSlug = project.slug;
  workspace.auditIdFromRoute = '';
  roadmapData.roadmapProjectId = undefined;
  applyBoardRoute(push ? new URLSearchParams() : new URLSearchParams(window.location.search));
  if (routeReleaseFilter) board.boardReleaseFilter = routeReleaseFilter;
  releasePlan.releaseSelectedId = '';
  releasePlan.releaseQueue = null;
  board.columns = [];
  board.tasks = [];
  bulk.selectedTaskIds = new Set();
  invalidateBulkRequest();
  bulk.bulkReviewTasks = [];
  bulk.showBulkModal = false;
  bulk.bulkResult = null;
  board.labels = [];
  invalidateBoardColumnRequests(Object.keys(board.boardPages));
  board.boardPages = {};
  board.boardPartial = false;
  board.boardReconciliationNotice = '';
  board.boardError = '';
  timeline.boardTimelineItems = [];
  timeline.boardTimelineNextCursor = '';
  timeline.boardTimelineError = '';
  timeline.boardTimelineProjectId = '';
  workspace.recentProjectIds = rememberProject(project.id, localStorage);
  localStorage.setItem(helmStorageKeys.lastProject, project.slug);
  nav.projectSwitcherOpen = false;
  closeCommandPalette();
  workspace.view = 'board';
  if (push) navigate(`/p/${encodeURIComponent(project.slug)}`);
  await loadBoard();
}

export async function setView(next: View, push = true): Promise<boolean> {
  if (hasPendingCreation()) return false;
  if (drawer.drawerTask && !closeDrawer(false, !push)) return false;
  workspace.view = next;
  nav.projectSwitcherOpen = false;
  nav.mobileNavOpen = false;
  closeNavMenus(false);
  closeCommandPalette();
  if (next === 'my-work') {
    if (push) navigate('/my-work');
    await loadMyWork();
  } else if (next === 'issues') {
    if (push) navigate('/issues');
    syncIssueViewURL(issues.issueFilters, issues.issueProjectFilter);
    await loadIssues();
  } else if (next === 'admin') {
    if (push) navigate('/admin');
  } else if (next === 'tickets') {
    roadmapData.roadmapProjectId = undefined;
    if (push) navigate('/tickets');
  } else if (next === 'roadmap') {
    roadmapData.roadmapProjectId = undefined;
    if (push) navigate('/roadmap');
    await loadRoadmap();
  } else if (next === 'search') {
    if (push) navigate(searchURL());
    await loadSearch();
  } else if (next === 'timeline' && workspace.activeProject) {
    roadmapData.roadmapProjectId = undefined;
    if (push) navigate(`/p/${encodeURIComponent(workspace.activeProject.slug)}/timeline`);
    if (!board.columns.length || timeline.boardTimelineProjectId !== workspace.activeProject.id) await loadBoard();
    await loadBoardTimeline(workspace.activeProject.id, { reset: timeline.boardTimelineProjectId !== workspace.activeProject.id });
  } else if (next === 'audits' && workspace.activeProject) {
    roadmapData.roadmapProjectId = undefined;
    workspace.auditIdFromRoute = '';
    if (push) navigate(`/p/${encodeURIComponent(workspace.activeProject.slug)}/audits`);
    // Audit apply needs the current task/column snapshot. Board data is
    // already warm in the common path; this guarded read fills it for a
    // direct project-audit navigation as well.
    if (!board.columns.length || !board.tasks.length) await loadBoard();
  } else if (next === 'releases' && workspace.activeProject) {
    roadmapData.roadmapProjectId = undefined;
    releasePlan.releaseSelectedId = '';
    releasePlan.releaseQueue = null;
    if (push) navigate(`/p/${encodeURIComponent(workspace.activeProject.slug)}/focus`);
    await loadReleasesPage();
  } else if (next === 'settings') {
    if (push) navigate('/settings');
    await Promise.all([loadAgents(), loadCodexAccount(), loadProjectAdmin()]);
  } else if (workspace.activeProject) {
    if (push) syncBoardViewURL();
    await loadBoard();
  }
  return workspace.view === next;
}

export function restoreDirtyTaskRoute() {
  if (!drawer.drawerTask) return;
  const project = projectForTask(drawer.drawerTask);
  if (!project) return;
  const path = taskDeepLink(project.slug, drawer.drawerTask.key, drawer.taskRouteIntent);
  navigate(path, true);
}

export function handleRootRoute() {
  const remembered = readMigratedStorage(localStorage, helmStorageKeys.lastProject, legacyRoadmapStorageKeys.lastProject);
  const target = workspace.projects.find((project) => project.slug === remembered)
    || workspace.projects.find((project) => project.slug === workspace.activeProjectSlug)
    || workspace.projects[0];
  roadmapData.roadmapProjectId = undefined;
  workspace.auditIdFromRoute = '';
  if (!target) {
    workspace.view = 'board';
    workspace.activeProjectSlug = '';
    board.columns = [];
    board.tasks = [];
    board.labels = [];
    return;
  }
  // Keep the root URL canonical while restoring the remembered/current
  // project board. selectProject(false) resets project-scoped state without
  // pushing the project URL back onto the history stack.
  if (workspace.activeProjectSlug !== target.slug) {
    void selectProject(target, false);
    return;
  }
  workspace.view = 'board';
  applyBoardRoute(new URLSearchParams());
  releasePlan.releaseSelectedId = '';
  releasePlan.releaseQueue = null;
  void loadBoard();
}

export function handlePopState() {
  if (hasPendingCreation()) {
    // The browser already changed the location. Keep the originating view,
    // full URL and drawer return state while this operation is unresolved.
    if (creation.pendingCreationRoute) window.history.replaceState(creation.pendingCreationRoute.state, '', creation.pendingCreationRoute.url);
    return;
  }
  if (drawer.drawerTask && drawer.drawerDraftDirty) {
    if (!confirmDrawerDiscard()) {
      restoreDirtyTaskRoute();
      return;
    }
    // The browser has already moved to the destination entry. Discard the
    // drawer without navigating back to its saved origin, then reconcile the
    // destination route below.
    closeDrawer(true, true);
  }
  if (drawer.drawerTask && !getTaskRouteFromLocation()) closeDrawer(true, true);
  const slug = getProjectSlugFromLocation();
  const taskRoute = getTaskRouteFromLocation();
  if (slug) {
    const project = workspace.projects.find((item) => item.slug === slug);
    if (!project) {
      navigate('/', true);
      handleRootRoute();
      return;
    }
    if (project && taskRoute) {
      const projectChanged = workspace.activeProjectSlug !== project.slug;
      workspace.projectSwitchVersion += 1;
      workspace.activeProjectSlug = project.slug;
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'board';
      if (projectChanged || !board.columns.length || !board.tasks.length) {
        board.columns = [];
        board.tasks = [];
        board.labels = [];
        void loadBoard().then(() => openTaskFromRoute(taskRoute, project));
      } else {
        void openTaskFromRoute(taskRoute, project);
      }
    } else if (project && isProjectTimelineLocation()) {
      const projectChanged = workspace.activeProjectSlug !== project.slug;
      workspace.projectSwitchVersion += 1;
      workspace.activeProjectSlug = project.slug;
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'timeline';
      if (projectChanged || !board.columns.length || !board.tasks.length) {
        board.columns = [];
        board.tasks = [];
        board.labels = [];
        void loadBoard().then(() => loadBoardTimeline(project.id, { reset: true }));
      } else {
        void loadBoardTimeline(project.id, { reset: timeline.boardTimelineProjectId !== project.id });
      }
    } else if (project && isProjectAuditLocation()) {
      const projectChanged = workspace.activeProjectSlug !== project.slug;
      workspace.projectSwitchVersion += 1;
      workspace.activeProjectSlug = project.slug;
      workspace.auditIdFromRoute = getAuditIdFromLocation();
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'audits';
      if (projectChanged || !board.columns.length || !board.tasks.length) {
        board.columns = [];
        board.tasks = [];
        board.labels = [];
        void loadBoard();
      }
    } else if (project && isProjectRoadmapLocation()) {
      workspace.projectSwitchVersion += 1;
      workspace.activeProjectSlug = project.slug;
      roadmapData.roadmapProjectId = project.id;
      workspace.view = 'roadmap';
      void loadRoadmap(project.id);
    } else if (project && isProjectReleasesLocation()) {
      canonicalizeProjectFocusLocation();
      workspace.projectSwitchVersion += 1;
      workspace.activeProjectSlug = project.slug;
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'releases';
      releasePlan.releaseSelectedId = new URL(window.location.href).searchParams.get('release') || '';
      void loadReleasesPage();
    } else if (project) void selectProject(project, false, new URL(window.location.href).searchParams.get('release') || 'all');
    return;
  }
  if (/^\/my-work\/?$/.test(window.location.pathname)) void setView('my-work', false);
  else if (/^\/search\/?$/.test(window.location.pathname)) {
    applySearchRouteFilters(new URL(window.location.href).searchParams);
    void setView('search', false);
  }
  else if (/^\/issues\/?$/.test(window.location.pathname)) {
    applyIssueRouteFilters(new URL(window.location.href).searchParams);
    void setView('issues', false);
  }
  else if (/^\/roadmap\/?$/.test(window.location.pathname)) void setView('roadmap', false);
  else if (/^\/settings\/?$/.test(window.location.pathname)) void setView('settings', false);
  else if (/^\/admin\/?$/.test(window.location.pathname)) void setView('admin', false);
  else if (/^\/tickets\/?$/.test(window.location.pathname)) void setView('tickets', false);
  else if (/^\/?$/.test(window.location.pathname)) handleRootRoute();
}

export async function openProjectTimeline() {
  await setView('timeline');
}

export async function openProjectAudits() {
  if (!workspace.activeProject) return;
  workspace.auditIdFromRoute = '';
  roadmapData.roadmapProjectId = undefined;
  workspace.view = 'audits';
  navigate(`/p/${encodeURIComponent(workspace.activeProject.slug)}/audits`);
  if (!board.columns.length || !board.tasks.length) await loadBoard();
}

export function navigateAudit(path: string) {
  const match = path.match(/^\/p\/[^/]+\/audits(?:\/([^/]+))?\/?$/);
  workspace.auditIdFromRoute = match?.[1] ? decodeURIComponent(match[1]) : '';
  workspace.view = 'audits';
  navigate(path);
}
