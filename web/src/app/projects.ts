// Project list loading, ordering, favorites and Luna project intelligence.
import { api } from '../lib/api';
import { readOfflineBoards, setOfflineOwner } from '../lib/offlineBoards';
import { helmStorageKeys, legacyRoadmapStorageKeys, readMigratedStorage } from '../lib/state';
import type { ProjectOrderMode } from '../lib/state';
import type { Project, ProjectIntelligenceRecommendation } from '../lib/types';
import { loadProjectAdmin } from './admin';
import { loadBoard } from './board/load';
import { applyBoardRoute } from './board/pages';
import { loadBoardTimeline } from './boardTimeline';
import { friendlyError, toast } from './feedback';
import { applyIssueRouteFilters, loadIssueMetrics, loadIssues } from './issues';
import { loadMyWork, loadSidebarCounts } from './myWork';
import { clearSavedBoards } from './offline';
import { loadReleasesPage } from './releases/load';
import { loadRoadmap } from './roadmap';
import { canonicalizeProjectFocusLocation, getAuditIdFromLocation, getProjectSlugFromLocation, getTaskRouteFromLocation, isProjectAuditLocation, isProjectReleasesLocation, isProjectRoadmapLocation, isProjectTimelineLocation, navigate, openTaskFromRoute } from './routing';
import { applySearchRouteFilters, loadSearch } from './search';
import { loadAgents, loadCodexAccount } from './settings';
import { board } from './state/board.svelte';
import { releasePlan } from './state/releasePlan.svelte';
import { roadmapData } from './state/roadmapData.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';

export async function loadProjects() {
  const requestId = ++workspace.projectListRequest;
  const selectionVersion = workspace.projectSwitchVersion;
  const requestedSession = session.sessionGeneration;
  workspace.projectsLoading = true;
  workspace.projectsError = '';
  try {
    const result = await api.listAllProjects();
    if (requestId !== workspace.projectListRequest || session.sessionGeneration !== requestedSession || !session.user) return;
    const nextProjects = result.data.filter((project) => !project.archived_at);
    const savedBoards = await readOfflineBoards();
    if (requestId !== workspace.projectListRequest || session.sessionGeneration !== requestedSession || !session.user) return;
    // A successful project listing is authoritative about access. Do not
    // retain snapshots of projects that were removed or became inaccessible.
    if (savedBoards.some((saved) => !nextProjects.some(project => project.id === saved.project.id))) {
      await clearSavedBoards();
      if (requestId !== workspace.projectListRequest || session.sessionGeneration !== requestedSession || !session.user) return;
      await setOfflineOwner(session.user.id);
      if (requestId !== workspace.projectListRequest || session.sessionGeneration !== requestedSession || !session.user) return;
    }
    workspace.projects = nextProjects;
    void loadProjectIntelligence();
    if (selectionVersion !== workspace.projectSwitchVersion) return;
    const routeSlug = getProjectSlugFromLocation();
    const routeProject = routeSlug ? nextProjects.find((project) => project.slug === routeSlug) : undefined;
    const unknownProjectRoute = Boolean(routeSlug && !routeProject);
    const taskRoute = getTaskRouteFromLocation();
    const remembered = readMigratedStorage(localStorage, helmStorageKeys.lastProject, legacyRoadmapStorageKeys.lastProject);
    // A project URL is an explicit scope. Falling back to the first project
    // for an unknown slug makes a mistyped/deleted URL look valid while
    // showing unrelated work. Canonicalize that URL to the workspace root,
    // then use the normal remembered-project fallback there.
    if (unknownProjectRoute) {
      navigate('/', true);
      workspace.view = 'board';
      roadmapData.roadmapProjectId = undefined;
      workspace.auditIdFromRoute = '';
    }
    const workProjects = nextProjects.filter((project) => !project.system_kind);
    const target = routeProject || workProjects.find((project) => project.slug === remembered) || workProjects[0];
    if (target) {
      workspace.activeProjectSlug = target.slug;
      const targetParams = new URL(window.location.href).searchParams;
      if (!taskRoute && !isProjectRoadmapLocation() && !isProjectReleasesLocation()) applyBoardRoute(targetParams);
      canonicalizeProjectFocusLocation();
      if (!isProjectReleasesLocation()) {
        board.boardReleaseFilter = targetParams.get('release') || 'all';
      }
      if ((workspace.view === 'board' && !isProjectReleasesLocation()) || routeProject && !isProjectReleasesLocation()) await loadBoard();
    } else {
      workspace.activeProjectSlug = '';
      board.columns = [];
      board.tasks = [];
      board.labels = [];
      if (!routeProject) workspace.view = 'board';
    }
    if (
      requestId !== workspace.projectListRequest
      || selectionVersion !== workspace.projectSwitchVersion
      || session.sessionGeneration !== requestedSession
      || !session.user
    ) return;
    // Navigation counts and issue health are deliberately scalar requests;
    // bootstrap never downloads the full Issues or My Work collections just
    // to render their badges.
    await Promise.all([loadIssueMetrics(), loadSidebarCounts()]);
    if (
      requestId !== workspace.projectListRequest
      || selectionVersion !== workspace.projectSwitchVersion
      || session.sessionGeneration !== requestedSession
      || !session.user
    ) return;
    const path = window.location.pathname;
    if (taskRoute && routeProject) {
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'board';
      await openTaskFromRoute(taskRoute, routeProject);
    } else if (/^\/my-work\/?$/.test(path)) {
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'my-work';
      await loadMyWork();
    } else if (/^\/issues\/?$/.test(path)) {
      roadmapData.roadmapProjectId = undefined;
      applyIssueRouteFilters(new URL(window.location.href).searchParams);
      workspace.view = 'issues';
      await loadIssues();
    } else if (/^\/roadmap\/?$/.test(path)) {
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'roadmap';
      await loadRoadmap();
    } else if (/^\/search\/?$/.test(path)) {
      roadmapData.roadmapProjectId = undefined;
      applySearchRouteFilters(new URL(window.location.href).searchParams);
      workspace.view = 'search';
      await loadSearch();
    } else if (/^\/admin\/?$/.test(path)) {
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'admin';
    } else if (/^\/tickets\/?$/.test(path)) {
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'tickets';
    } else if (/^\/settings\/?$/.test(path)) {
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'settings';
      await Promise.all([loadAgents(), loadCodexAccount(), loadProjectAdmin()]);
    } else if (routeProject && isProjectAuditLocation()) {
      roadmapData.roadmapProjectId = undefined;
      workspace.auditIdFromRoute = getAuditIdFromLocation();
      workspace.view = 'audits';
    } else if (routeProject && isProjectTimelineLocation()) {
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'timeline';
      await loadBoardTimeline(routeProject.id, { reset: true });
    } else if (routeProject && isProjectRoadmapLocation()) {
      roadmapData.roadmapProjectId = routeProject.id;
      workspace.view = 'roadmap';
      await loadRoadmap(roadmapData.roadmapProjectId);
    } else if (routeProject && isProjectReleasesLocation()) {
      roadmapData.roadmapProjectId = undefined;
      workspace.view = 'releases';
      releasePlan.releaseSelectedId = new URL(window.location.href).searchParams.get('release') || '';
      await loadReleasesPage();
    }
  } catch (error) {
    if (requestId === workspace.projectListRequest && session.sessionGeneration === requestedSession && session.user) {
      workspace.projectsError = friendlyError(error, 'Projects could not be loaded.');
    }
  } finally {
    if (requestId === workspace.projectListRequest && session.sessionGeneration === requestedSession) workspace.projectsLoading = false;
  }
}

export function setProjectOrderMode(mode: ProjectOrderMode) {
  workspace.projectOrderMode = mode;
  localStorage.setItem(helmStorageKeys.projectOrder, mode);
}

export function projectAttentionLabel(recommendation?: ProjectIntelligenceRecommendation): string {
  if (!recommendation) return '';
  if (recommendation.attention === 'act_now') return 'Act now';
  if (recommendation.attention === 'watch') return 'Watch';
  if (recommendation.attention === 'steady') return 'Steady';
  return 'Quiet';
}

export async function loadProjectIntelligence() {
  if (workspace.projectIntelligenceLoading || !session.user) return;
  workspace.projectIntelligenceLoading = true;
  workspace.projectIntelligenceError = '';
  try {
    workspace.projectIntelligence = await api.projectIntelligence();
  } catch (error) {
    workspace.projectIntelligenceError = friendlyError(error, 'Project intelligence could not be loaded.');
  } finally {
    workspace.projectIntelligenceLoading = false;
  }
}

export async function analyzeProjectIntelligence() {
  if (workspace.projectIntelligenceAnalyzing) return;
  const controller = new AbortController();
  workspace.projectIntelligenceController = controller;
  workspace.projectIntelligenceAnalyzing = true;
  workspace.projectIntelligenceError = '';
  try {
    workspace.projectIntelligence = await api.analyzeProjectIntelligence(controller.signal);
    setProjectOrderMode('smart');
    toast('success', 'Luna refreshed the Smart project order.');
  } catch (error) {
    if (controller.signal.aborted) {
      workspace.projectIntelligenceError = 'Luna analysis was canceled. The metric order is still available.';
      return;
    }
    workspace.projectIntelligenceError = friendlyError(error, 'Luna could not analyze the projects.');
    toast('error', workspace.projectIntelligenceError);
  } finally {
    if (workspace.projectIntelligenceController === controller) workspace.projectIntelligenceController = undefined;
    workspace.projectIntelligenceAnalyzing = false;
  }
}

export function cancelProjectIntelligence() {
  workspace.projectIntelligenceController?.abort();
}

export async function toggleFavorite(event: MouseEvent, project: Project) {
  event.stopPropagation();
  try {
    const updated = await api.patchProject(project.id, { favorite: !project.favorite });
    workspace.projects = workspace.projects.map((item) => (item.id === project.id ? { ...item, ...updated } : item));
  } catch (error) {
    toast('error', friendlyError(error, 'Favorite status could not be saved.'));
  }
}
