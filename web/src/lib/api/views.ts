import type {
  AdminMetrics,
  Collection,
  IssueMetrics,
  Project,
  RoadmapSummary,
  SavedView,
  SavedViewFilters,
  SearchResponse,
  SearchSort,
  SidebarCounts,
  Task
} from '../types';
import type { MyWorkParams, SearchParams, WorkView } from './params';
import { collectPages, collectionFrom, key, pathWithQuery, request } from './client';

interface ViewDelegates {
  search(params?: SearchParams): Promise<{
    data: Task[];
    next_cursor: string | null;
    projects?: Project[];
    views?: SavedView[];
  }>;
}

export function createViewsApi(getApi: () => ViewDelegates) {
  return {
    myWork: (params: MyWorkParams = {}) =>
      request<Collection<Task> | Task[]>(
        pathWithQuery('/my-work', {
          project: params.project,
          state: params.state,
          priority: params.priority,
          label: params.label,
          q: params.q,
          updated_after: params.updated_after,
          view: params.view,
          agent_state: params.agent_state,
          action_needed: params.action_needed,
          dependency: params.dependency,
          release_id: params.release_id ?? params.release,
          cursor: params.cursor,
          limit: params.limit
        })
      ).then(collectionFrom),

    allMyWork: (params: MyWorkParams = {}) => collectPages((cursor) =>
      request<Collection<Task> | Task[]>(
        pathWithQuery('/my-work', {
          project: params.project,
          state: params.state,
          priority: params.priority,
          label: params.label,
          q: params.q,
          updated_after: params.updated_after,
          view: params.view,
          agent_state: params.agent_state,
          action_needed: params.action_needed,
          dependency: params.dependency,
          release_id: params.release_id ?? params.release,
          cursor,
          limit: params.limit ?? 200
        })
      ).then(collectionFrom), params.cursor
    ),

    adminMetrics: (days = 7) => request<AdminMetrics>(pathWithQuery('/admin/metrics', { days })),

    issueMetrics: (params: { project?: string } = {}) =>
      request<IssueMetrics>(pathWithQuery('/issues/metrics', { project: params.project })),

    sidebarCounts: (params: { project?: string; view?: WorkView } = {}) =>
      request<SidebarCounts>(pathWithQuery('/sidebar-counts', { project: params.project, view: params.view })),

    search: (params: SearchParams = {}) =>
      request<SearchResponse>(
        pathWithQuery('/search', {
          clear: params.clear,
          q: params.q,
          key: params.key,
          title: params.title,
          description: params.description,
          label: params.label,
          state: params.state,
          priority: params.priority,
          assignee: params.assignee,
          claim_owner: params.claim_owner,
          release_id: params.release_id,
          project: params.project,
          due_from: params.due_from,
          due_to: params.due_to,
          sort: params.sort,
          view: params.view,
          cursor: params.cursor,
          limit: params.limit
        })
      ).then((result) => ({ ...result, data: result?.data ?? [], next_cursor: result?.next_cursor ?? null })),

    searchSavedView: (view: string, params: Omit<SearchParams, 'view'> = {}) =>
      getApi().search({ ...params, view }),

    listSavedViews: (params: { cursor?: string; limit?: number } = {}) =>
      request<Collection<SavedView> | SavedView[]>(pathWithQuery('/views', { cursor: params.cursor, limit: params.limit })).then(collectionFrom),

    listAllSavedViews: () => collectPages((cursor) =>
      request<Collection<SavedView> | SavedView[]>(pathWithQuery('/views', { cursor, limit: 200 })).then(collectionFrom)
    ),

    getSavedView: (view: string) => request<SavedView>(`/views/${encodeURIComponent(view)}`),

    createSavedView: (input: { name: string; description?: string; filters: SavedViewFilters; sort?: SearchSort[]; shared?: boolean }) =>
      request<SavedView>('/views', { method: 'POST', body: input, idempotencyKey: key() }),

    patchSavedView: (view: string, input: Partial<{ name: string; description: string; filters: SavedViewFilters; sort: SearchSort[]; shared: boolean }>) =>
      request<SavedView>(`/views/${encodeURIComponent(view)}`, { method: 'PATCH', body: input, idempotencyKey: key() }),

    deleteSavedView: (view: string) =>
      request<void>(`/views/${encodeURIComponent(view)}`, { method: 'DELETE', idempotencyKey: key() }),

    roadmap: (project?: string) =>
      request<RoadmapSummary>(project ? `/projects/${encodeURIComponent(project)}/roadmap` : '/roadmap')
  };
}
