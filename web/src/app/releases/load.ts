// Focus (release) data loading and helpers.
import { get as readStore } from 'svelte/store';
import { api } from '../../lib/api';
import { mapWithConcurrency, projectFanOut } from '../../lib/concurrency';
import { offlineReadOnly } from '../../lib/connectivity';
import { loadBoard } from '../board/load';
import { openWorkTask } from '../drawer/open';
import { announce, friendlyError, toast } from '../feedback';
import { navigate } from '../routing';
import { board } from '../state/board.svelte';
import { issues } from '../state/issues.svelte';
import { myWork } from '../state/myWork.svelte';
import { releasePlan } from '../state/releasePlan.svelte';
import { search } from '../state/search.svelte';
import { session } from '../state/session.svelte';
import { workspace } from '../state/workspace.svelte';
import type { ReleaseModel, ReleaseQueueModel, ReleaseSummaryModel } from '../types';

export function releaseApiFunction(name: string): ((...args: unknown[]) => Promise<unknown>) | null {
  const candidate = (api as unknown as Record<string, unknown>)[name];
  return typeof candidate === 'function' ? candidate as (...args: unknown[]) => Promise<unknown> : null;
}

export async function callReleaseApi<T>(name: string, ...args: unknown[]): Promise<T> {
  const method = releaseApiFunction(name);
  if (!method) throw new Error('Focus planning is unavailable until the API client is updated.');
  return await method(...args) as T;
}

export function releaseCollectionData(payload: unknown): ReleaseModel[] {
  if (Array.isArray(payload)) return payload as ReleaseModel[];
  if (payload && typeof payload === 'object' && Array.isArray((payload as { data?: unknown }).data)) {
    return (payload as { data: ReleaseModel[] }).data;
  }
  return [];
}

export function releaseSummary(release: ReleaseModel | null | undefined): ReleaseSummaryModel {
  return release?.summary || {};
}

export function releaseProgress(release: ReleaseModel | null | undefined, required = false): number {
  const summary = releaseSummary(release);
  const complete = required ? summary.required_completed_count : summary.completed_count;
  const total = required ? summary.required_task_count : summary.task_count;
  if (!total) return 0;
  return Math.round(Math.max(0, Math.min(100, ((complete || 0) / total) * 100)));
}

export function releaseStatusLabel(release: ReleaseModel | null | undefined): string {
  return release?.status === 'released' ? 'Completed' : 'Active';
}

export function releaseFilterLabel(value: string): string {
  if (value === 'all') return 'All focus areas';
  if (value === 'unassigned') return 'No focus';
  return releasePlan.allReleaseOptions.find((release) => release.id === value)?.name
    || releasePlan.activeProjectReleases.find((release) => release.id === value)?.name
    || 'Focus';
}

export function sortReleaseList(items: ReleaseModel[]): ReleaseModel[] {
  return [...items].sort((left, right) => {
    const leftReleased = left.status === 'released' ? 1 : 0;
    const rightReleased = right.status === 'released' ? 1 : 0;
    if (leftReleased !== rightReleased) return leftReleased - rightReleased;
    const leftDate = left.target_date ? Date.parse(left.target_date) : Number.POSITIVE_INFINITY;
    const rightDate = right.target_date ? Date.parse(right.target_date) : Number.POSITIVE_INFINITY;
    if (leftDate !== rightDate) return leftDate - rightDate;
    const leftUpdated = left.updated_at ? Date.parse(left.updated_at) : 0;
    const rightUpdated = right.updated_at ? Date.parse(right.updated_at) : 0;
    return rightUpdated - leftUpdated || left.name.localeCompare(right.name);
  });
}

export function releaseWritesDisabled(): boolean {
  return readStore(offlineReadOnly) || (typeof navigator !== 'undefined' && !navigator.onLine);
}

export async function loadAccessibleReleases(projectSnapshot = workspace.projects): Promise<void> {
  if (!releaseApiFunction('listReleases')) return;
  await mapWithConcurrency(projectSnapshot, projectFanOut, (project) => loadProjectReleases(project.id).catch(() => []));
}

export async function loadProjectReleases(projectId = workspace.activeProject?.id, page = false): Promise<ReleaseModel[]> {
  if (!projectId) return [];
  if (page) {
    releasePlan.releasesLoading = true;
    releasePlan.releasesError = '';
  }
  const requestedProjectId = projectId;
  const requestedSession = session.sessionGeneration;
  const requestId = (releasePlan.releaseRequestsByProject[requestedProjectId] || 0) + 1;
  releasePlan.releaseRequestsByProject = { ...releasePlan.releaseRequestsByProject, [requestedProjectId]: requestId };
  try {
    const listMethod = releaseApiFunction('listAllReleases') || releaseApiFunction('listReleases');
    if (!listMethod) throw new Error('Focus planning is unavailable until the API client is updated.');
    const payload = await listMethod(projectId, { limit: 200 });
    const items = releaseCollectionData(payload);
    if (
      releasePlan.releaseRequestsByProject[requestedProjectId] !== requestId
      || session.sessionGeneration !== requestedSession
      || !session.user
      || (page && workspace.activeProject?.id !== requestedProjectId)
    ) return items;
    releasePlan.releasesByProject = { ...releasePlan.releasesByProject, [requestedProjectId]: items };
    if (workspace.activeProject?.id === requestedProjectId) releasePlan.releases = items;
    if (page && board.boardReleaseFilter !== 'all' && board.boardReleaseFilter !== 'unassigned' && !items.some((item) => item.id === board.boardReleaseFilter)) {
      board.boardReleaseFilter = 'all';
      announce('The focus filter was cleared because it is not part of this project.');
    }
    return items;
  } catch (error) {
    if (page && releasePlan.releaseRequestsByProject[requestedProjectId] === requestId && session.sessionGeneration === requestedSession && session.user) releasePlan.releasesError = friendlyError(error, 'Focus areas could not be loaded.');
    return [];
  } finally {
    if (page && releasePlan.releaseRequestsByProject[requestedProjectId] === requestId && session.sessionGeneration === requestedSession) releasePlan.releasesLoading = false;
  }
}

export async function loadReleaseQueue(releaseId = releasePlan.releaseSelectedId): Promise<boolean> {
  if (!releaseId) {
    releasePlan.releaseQueue = null;
    return true;
  }
  const requestId = ++releasePlan.releaseQueueRequest;
  const requestedSession = session.sessionGeneration;
  const requestedReleaseId = releaseId;
  releasePlan.releaseQueueLoading = true;
  releasePlan.releaseQueueError = '';
  try {
    const method = releaseApiFunction('getAllReleaseWorkQueue') || releaseApiFunction('getReleaseWorkQueue') || releaseApiFunction('releaseWorkQueue') || releaseApiFunction('listReleaseWorkQueue');
    if (!method) throw new Error('The focus work queue is not available yet.');
    const result = await method(requestedReleaseId, { limit: 200 });
    if (requestId !== releasePlan.releaseQueueRequest || session.sessionGeneration !== requestedSession || releasePlan.releaseSelectedId !== requestedReleaseId || !session.user) return false;
    releasePlan.releaseQueue = result as ReleaseQueueModel;
    return true;
  } catch (error) {
    if (requestId === releasePlan.releaseQueueRequest && session.sessionGeneration === requestedSession && releasePlan.releaseSelectedId === requestedReleaseId && session.user) releasePlan.releaseQueueError = friendlyError(error, 'The focus work queue could not be loaded.');
    return false;
  } finally {
    if (requestId === releasePlan.releaseQueueRequest && session.sessionGeneration === requestedSession) releasePlan.releaseQueueLoading = false;
  }
}

export async function loadReleasesPage(): Promise<boolean> {
  if (!workspace.activeProject) return false;
  const loaded = await loadProjectReleases(workspace.activeProject.id, true);
  if (workspace.activeProject?.id !== loaded[0]?.project_id && !loaded.length && releasePlan.releasesByProject[workspace.activeProject.id] === undefined) return false;
  if (releasePlan.releaseSelectedId && !loaded.some((release) => release.id === releasePlan.releaseSelectedId)) {
    releasePlan.releaseSelectedId = '';
    const params = new URLSearchParams(window.location.search);
    params.delete('release');
    navigate(`/p/${encodeURIComponent(workspace.activeProject.slug)}/focus${params.toString() ? `?${params.toString()}` : ''}`, true);
    announce('The selected focus was cleared because it is not part of this project.');
  }
  const next = loaded.find((release) => release.id === releasePlan.releaseSelectedId)
    || loaded.find((release) => release.status === 'planned')
    || loaded[0];
  releasePlan.releaseSelectedId = next?.id || '';
  return await loadReleaseQueue(releasePlan.releaseSelectedId);
}

export async function openProjectReleases() {
  if (!workspace.activeProject) return;
  releasePlan.releaseSelectedId = '';
  releasePlan.releaseQueue = null;
  workspace.view = 'releases';
  navigate(`/p/${encodeURIComponent(workspace.activeProject.slug)}/focus`);
  await loadReleasesPage();
}

export function selectRelease(release: ReleaseModel) {
  releasePlan.releaseSelectedId = release.id;
  releasePlan.releaseQueue = null;
  if (workspace.activeProject && workspace.view === 'releases') navigate(`/p/${encodeURIComponent(workspace.activeProject.slug)}/focus?release=${encodeURIComponent(release.id)}`, true);
  void loadReleaseQueue(release.id);
}

export async function openReleaseFilteredBoard(release: ReleaseModel) {
  const project = workspace.projects.find((item) => item.id === release.project_id);
  if (!project) {
    toast('error', 'This focus belongs to a project that is no longer available.');
    return;
  }
  workspace.activeProjectSlug = project.slug;
  board.boardReleaseFilter = release.id;
  workspace.view = 'board';
  navigate(`/p/${encodeURIComponent(project.slug)}?release=${encodeURIComponent(release.id)}`);
  await loadBoard();
}

export async function openWorkTaskById(taskId: string): Promise<void> {
  const known = [...board.tasks, ...myWork.myWorkTasks, ...issues.issueTasks, ...search.searchTasks].find((task) => task.id === taskId);
  try {
    await openWorkTask(known || await api.getTask(taskId));
  } catch (error) {
    toast('error', friendlyError(error, 'The queue task could not be opened.'));
  }
}
