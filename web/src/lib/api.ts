import { adminApi } from './api/admin';
import { intelligenceApi } from './api/intelligence';
import { projectsApi } from './api/projects';
import { createReleasesApi } from './api/releases';
import { createTasksApi } from './api/tasks';
import { ticketsApi } from './api/tickets';
import { intakeApi } from './api/intake';
import { activityApi } from './api/activity';
import { createViewsApi } from './api/views';
import { auditsApi } from './api/audits';
import { githubApi } from './api/github';
import { membershipApi } from './api/membership';
import { collectPages } from './api/client';
import type { Actor, Collection, Task, Release } from './types';
import type { TaskListParams, IssueListParams, ReleaseListParams } from './api/params';

export { API_PREFIX, pendingMutationCount, request, portableImportReportFromError, etagForVersion, collectionFrom } from './api/client';
export type { RequestOptions } from './api/client';
export type * from './api/params';

// Preserve the public API surface while each domain owns its endpoint behavior.
export const api = {
  ...adminApi,
  ...intelligenceApi,
  ...projectsApi,
  ...createReleasesApi((): ReturnType<typeof createReleasesApi> => api),
  ...createTasksApi((): ReturnType<typeof createTasksApi> => api),
  ...ticketsApi,
  ...intakeApi,
  ...activityApi,
  ...createViewsApi((): ReturnType<typeof createViewsApi> => api),
  ...auditsApi,
  ...githubApi,
  ...membershipApi
};

/** Fetch every page for a board while retaining the contract's cursor API. */
export async function listAllTasks(project: string, params: TaskListParams = {}): Promise<Collection<Task>> {
  return collectPages((cursor) => api.listTasks(project, { ...params, cursor, limit: params.limit ?? 200 }), params.cursor);
}

export async function listAllIssues(params: IssueListParams = {}): Promise<Collection<Task>> {
  return collectPages((cursor) => api.listIssues({ ...params, cursor, limit: params.limit ?? 200 }), params.cursor);
}

/** Fetch every release page while retaining the public cursor API. */
export async function listAllProjectReleases(project: string, params: ReleaseListParams = {}): Promise<Collection<Release>> {
  return collectPages((cursor) => api.listProjectReleases(project, { ...params, cursor, limit: params.limit ?? 200 }), params.cursor);
}

/** Concise top-level alias matching the api object's release collection helper. */
export const listAllReleases = listAllProjectReleases;

export function unwrapActor(value: Actor | { user: Actor }): Actor {
  return 'user' in value ? value.user : value;
}
