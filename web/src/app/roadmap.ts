// Roadmap overview and project progress loading.
import { api } from '../lib/api';
import { mapWithConcurrency, projectFanOut } from '../lib/concurrency';
import { helmStorageKeys, rememberProject, taskDeepLink } from '../lib/state';
import type { ActivityEvent, Task } from '../lib/types';
import { loadBoard } from './board/load';
import { openTask } from './drawer/open';
import { friendlyError, observeWorkTransitions } from './feedback';
import { navigate } from './routing';
import { board } from './state/board.svelte';
import { drawer } from './state/drawer.svelte';
import { myWork } from './state/myWork.svelte';
import { roadmapData } from './state/roadmapData.svelte';
import { session } from './state/session.svelte';
import { settings } from './state/settings.svelte';
import { workspace } from './state/workspace.svelte';
import { projectForTask } from './taskQueries';

export async function loadRoadmap(projectId = roadmapData.roadmapProjectId): Promise<boolean> {
  const requestId = ++roadmapData.roadmapRequest;
  const requestedProjectId = projectId;
  roadmapData.roadmapLoading = true;
  roadmapData.roadmapError = '';
  try {
    const result = await api.roadmap(projectId);
    if (requestId !== roadmapData.roadmapRequest || workspace.view !== 'roadmap' || roadmapData.roadmapProjectId !== requestedProjectId) return false;
    // Keep the UI aliases compatible with both the contract names and the
    // compact names emitted by the current server implementation.
    roadmapData.roadmap = {
      ...result,
      completed_count: result.completed_count ?? result.completed,
      overdue_count: result.overdue_count ?? result.overdue,
      due_soon_count: result.due_soon_count ?? result.due_soon,
      upcoming_tasks: result.upcoming_tasks ?? result.upcoming
    };
    if (!projectId && result.projects?.length) {
      workspace.projects = workspace.projects.map((project) => {
        const summary = result.projects?.find((row) => row.project.id === project.id);
        return summary
          ? { ...project, task_count: summary.total_tasks, completed_task_count: summary.completed_tasks }
          : project;
        });
    } else if (!projectId && workspace.projects.length) {
      // The v1 response may omit per-project rows. Fill the progress list
      // from the documented project-scoped roadmap endpoint when needed.
      const summaries = await mapWithConcurrency(workspace.projects, projectFanOut, async (project) => {
        try {
          return { projectId: project.id, summary: await api.roadmap(project.slug) };
        } catch {
          return null;
        }
      });
      if (requestId !== roadmapData.roadmapRequest || workspace.view !== 'roadmap' || roadmapData.roadmapProjectId !== requestedProjectId) return false;
      workspace.projects = workspace.projects.map((project) => {
        const row = summaries.find((summary) => summary?.projectId === project.id)?.summary;
        return row
          ? { ...project, task_count: row.task_total ?? row.total_tasks ?? project.task_count, completed_task_count: row.completed ?? row.completed_count ?? project.completed_task_count }
          : project;
      });
    }
    // These are intentionally separate reads from the aggregate summary:
    // live work is task-scoped and activity task metadata is not embedded in
    // the append-only event records. Both reads are guarded independently so
    // a route change cannot paint another scope over the current Roadmap.
    void loadRoadmapLiveWork(projectId, requestId);
    void loadRoadmapActivityTasks(result.recent_activity || [], projectId, requestId);
    void loadRoadmapActors(requestId);
    return true;
  } catch (error) {
    if (requestId === roadmapData.roadmapRequest && workspace.view === 'roadmap' && roadmapData.roadmapProjectId === requestedProjectId) {
      roadmapData.roadmapError = friendlyError(error, 'Roadmap progress could not be loaded.');
    }
    return false;
  } finally {
    if (requestId === roadmapData.roadmapRequest) roadmapData.roadmapLoading = false;
  }
}

export async function loadRoadmapLiveWork(projectId: string | undefined, parentRequestId: number): Promise<void> {
  const requestId = ++roadmapData.roadmapLiveRequest;
  const requestedSession = session.sessionGeneration;
  const scopeProjects = projectId
    ? workspace.projects.filter((project) => project.id === projectId)
    : [...workspace.projects];
  roadmapData.roadmapLiveLoading = true;
  roadmapData.roadmapLiveError = '';
  try {
    const [workResult, columnResults] = await Promise.all([
      api.allMyWork({ view: 'live', ...(projectId ? { project: projectId } : {}) }),
      Promise.all(scopeProjects.map(async (project) => ({
        projectId: project.id,
        columns: (await api.listAllColumns(project.id)).data
      })))
    ]);
    if (
      requestId !== roadmapData.roadmapLiveRequest
      || parentRequestId !== roadmapData.roadmapRequest
      || requestedSession !== session.sessionGeneration
      || workspace.view !== 'roadmap'
      || roadmapData.roadmapProjectId !== projectId
      || !session.user
    ) return;
    roadmapData.roadmapLiveTasks = workResult.data;
    roadmapData.roadmapLiveColumnsByProject = Object.fromEntries(columnResults.map((result) => [result.projectId, result.columns]));
    observeWorkTransitions(roadmapData.roadmapLiveTasks);
  } catch (error) {
    if (
      requestId === roadmapData.roadmapLiveRequest
      && parentRequestId === roadmapData.roadmapRequest
      && requestedSession === session.sessionGeneration
      && workspace.view === 'roadmap'
      && roadmapData.roadmapProjectId === projectId
      && session.user
    ) {
      roadmapData.roadmapLiveError = friendlyError(error, 'Live agent work could not be loaded.');
    }
  } finally {
    if (requestId === roadmapData.roadmapLiveRequest) roadmapData.roadmapLiveLoading = false;
  }
}

export function knownRoadmapActivityTasks(): Record<string, Task> {
  const known: Record<string, Task> = { ...roadmapData.roadmapActivityTasks };
  [...board.tasks, ...myWork.myWorkTasks, ...roadmapData.roadmapLiveTasks, ...roadmapData.roadmapUpcoming].forEach((task) => {
    known[task.id] = task;
  });
  return known;
}

export async function loadRoadmapActivityTasks(
  activity: ActivityEvent[],
  projectId: string | undefined,
  parentRequestId: number
): Promise<void> {
  const requestId = ++roadmapData.roadmapActivityRequest;
  const requestedSession = session.sessionGeneration;
  const known = knownRoadmapActivityTasks();
  const taskIds = Array.from(new Set(activity.map((event) => event.task_id).filter((id): id is string => Boolean(id))));
  const missing = taskIds.filter((taskId) => !known[taskId]);
  roadmapData.roadmapActivityLoading = missing.length > 0;
  roadmapData.roadmapActivityError = '';
  if (!missing.length) {
    if (
      requestId === roadmapData.roadmapActivityRequest
      && parentRequestId === roadmapData.roadmapRequest
      && requestedSession === session.sessionGeneration
      && workspace.view === 'roadmap'
      && roadmapData.roadmapProjectId === projectId
    ) roadmapData.roadmapActivityTasks = known;
    roadmapData.roadmapActivityLoading = false;
    return;
  }
  const fetched = await Promise.all(missing.map(async (taskId) => {
    try {
      return await api.getTask(taskId);
    } catch {
      return null;
    }
  }));
  if (
    requestId !== roadmapData.roadmapActivityRequest
    || parentRequestId !== roadmapData.roadmapRequest
    || requestedSession !== session.sessionGeneration
    || workspace.view !== 'roadmap'
    || roadmapData.roadmapProjectId !== projectId
  ) return;
  fetched.filter((task): task is Task => Boolean(task)).forEach((task) => {
    if (!projectId || task.project_id === projectId) known[task.id] = task;
  });
  roadmapData.roadmapActivityTasks = known;
  roadmapData.roadmapActivityLoading = false;
}

export function seedRoadmapActors() {
  const next = { ...roadmapData.roadmapActors };
  if (session.user) next[session.user.id] = { id: session.user.id, kind: session.user.kind, name: session.user.name };
  settings.agents.forEach((agent) => {
    next[agent.id] = { id: agent.id, kind: agent.kind, name: agent.name };
  });
  roadmapData.roadmapActors = next;
}

export async function loadRoadmapActors(parentRequestId: number): Promise<void> {
  seedRoadmapActors();
  if (roadmapData.roadmapActorsLoaded || roadmapData.roadmapActorsLoading) return;
  roadmapData.roadmapActorsLoading = true;
  const requestedSession = session.sessionGeneration;
  try {
    const result = await api.listAllAgents();
    if (requestedSession !== session.sessionGeneration || !session.user) return;
    result.data.forEach((agent) => {
      roadmapData.roadmapActors[agent.id] = { id: agent.id, kind: agent.kind, name: agent.name };
    });
    seedRoadmapActors();
    roadmapData.roadmapActorsLoaded = true;
  } catch {
    // A non-admin can still use Roadmap; opaque actor IDs remain explicit
    // fallbacks when the agent directory is not readable.
  } finally {
    if (requestedSession === session.sessionGeneration && parentRequestId <= roadmapData.roadmapRequest) roadmapData.roadmapActorsLoading = false;
  }
}

export async function openProjectRoadmap() {
  if (!workspace.activeProject) return;
  roadmapData.roadmapProjectId = workspace.activeProject.id;
  workspace.view = 'roadmap';
  navigate(`/p/${encodeURIComponent(workspace.activeProject.slug)}/roadmap`);
  await loadRoadmap(workspace.activeProject.id);
}

export async function openRoadmapTask(task: Task): Promise<void> {
  const project = projectForTask(task) || workspace.projects.find((item) => item.id === task.project_id);
  if (!project) {
    await openTask(task, 'activity');
    return;
  }
  drawer.taskRouteOrigin = window.location.pathname + window.location.search;
  workspace.activeProjectSlug = project.slug;
  roadmapData.roadmapProjectId = undefined;
  workspace.view = 'board';
  workspace.recentProjectIds = rememberProject(project.id, localStorage);
  localStorage.setItem(helmStorageKeys.lastProject, project.slug);
  navigate(taskDeepLink(project.slug, task.key, 'activity'));
  await loadBoard();
  await openTask(task, 'activity');
}

export async function openRoadmapActivity(event: ActivityEvent): Promise<void> {
  const task = event.task_id ? roadmapData.roadmapActivityTasks[event.task_id] : undefined;
  if (task) {
    await openRoadmapTask(task);
    return;
  }
  if (!event.task_id) return;
  try {
    const loaded = await api.getTask(event.task_id);
    if (roadmapData.roadmapProjectId && loaded.project_id !== roadmapData.roadmapProjectId) return;
    roadmapData.roadmapActivityTasks = { ...roadmapData.roadmapActivityTasks, [loaded.id]: loaded };
    await openRoadmapTask(loaded);
  } catch {
    roadmapData.roadmapActivityError = 'This activity points to a task that is no longer available.';
  }
}
