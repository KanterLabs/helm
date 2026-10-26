import {
  ApiError,
  type Collection,
  type Release,
  type ReleaseCreateInput,
  type ReleasePatchInput,
  type ReleaseReopenInput,
  type ReleaseWorkQueue
} from '../types';
import type { ReleaseListParams, ReleaseWorkQueueParams } from './params';
import { collectPages, collectionFrom, key, pathWithQuery, request } from './client';

interface ReleaseDelegates {
  listProjectReleases(project: string, params?: ReleaseListParams): Promise<Collection<Release>>;
  listAllProjectReleases(project: string, params?: ReleaseListParams): Promise<Collection<Release>>;
  createProjectRelease(project: string, input: ReleaseCreateInput): Promise<Release>;
  patchRelease(release: string, input: ReleasePatchInput, version: number): Promise<Release>;
  getReleaseWorkQueue(release: string, params?: ReleaseWorkQueueParams): Promise<ReleaseWorkQueue>;
}

export function createReleasesApi(getApi: () => ReleaseDelegates) {
  return {
    /** List project-local product releases, including planned and released history. */
    listProjectReleases: (project: string, params: ReleaseListParams = {}) =>
      request<Collection<Release> | Release[]>(
        pathWithQuery(`/projects/${encodeURIComponent(project)}/releases`, {
          status: params.status,
          target_from: params.target_from,
          target_to: params.target_to,
          cursor: params.cursor,
          limit: params.limit
        })
      ).then(collectionFrom),

    /** Short alias for project-scoped release discovery. */
    listReleases: (project: string, params: ReleaseListParams = {}) =>
      getApi().listProjectReleases(project, params),

    listAllProjectReleases: (project: string, params: ReleaseListParams = {}) => collectPages((cursor) =>
      getApi().listProjectReleases(project, { ...params, cursor, limit: params.limit ?? 200 }), params.cursor
    ),

    listAllReleases: (project: string, params: ReleaseListParams = {}) =>
      getApi().listAllProjectReleases(project, params),

    createProjectRelease: (project: string, input: ReleaseCreateInput) =>
      request<Release>(`/projects/${encodeURIComponent(project)}/releases`, {
        method: 'POST',
        body: input,
        idempotencyKey: key()
      }),

    /** Short alias for creating a project-local product release. */
    createRelease: (project: string, input: ReleaseCreateInput) =>
      getApi().createProjectRelease(project, input),

    getRelease: (release: string) =>
      request<Release>(`/releases/${encodeURIComponent(release)}`),

    patchRelease: (release: string, input: ReleasePatchInput, version: number) =>
      request<Release>(`/releases/${encodeURIComponent(release)}`, {
        method: 'PATCH',
        body: input,
        ifMatch: version,
        idempotencyKey: key()
      }),

    /** OpenAPI operation-name alias for patchRelease. */
    updateRelease: (release: string, input: ReleasePatchInput, version: number) =>
      getApi().patchRelease(release, input, version),

    deleteRelease: (release: string, version: number) =>
      request<void>(`/releases/${encodeURIComponent(release)}`, {
        method: 'DELETE',
        ifMatch: version,
        idempotencyKey: key()
      }),

    completeRelease: (release: string, version: number) =>
      request<Release>(`/releases/${encodeURIComponent(release)}/complete`, {
        method: 'POST',
        ifMatch: version,
        idempotencyKey: key()
      }),

    reopenRelease: (release: string, version: number, input: ReleaseReopenInput | string) =>
      request<Release>(`/releases/${encodeURIComponent(release)}/reopen`, {
        method: 'POST',
        body: typeof input === 'string' ? { reason: input } : input,
        ifMatch: version,
        idempotencyKey: key()
      }),

    getReleaseWorkQueue: (release: string, params: ReleaseWorkQueueParams = {}) =>
      request<ReleaseWorkQueue>(
        pathWithQuery(`/releases/${encodeURIComponent(release)}/work-queue`, {
          cursor: params.cursor,
          limit: params.limit
        })
      ),

    getAllReleaseWorkQueue: async (release: string): Promise<ReleaseWorkQueue> => {
      let cursor: string | undefined;
      let first: ReleaseWorkQueue | undefined;
      const data: ReleaseWorkQueue['data'] = [];
      const seen = new Set<string>();
      for (let page = 0; page < 10; page += 1) {
        const result = await getApi().getReleaseWorkQueue(release, { cursor, limit: 200 });
        first ??= result;
        data.push(...result.data);
        if (!result.next_cursor) return { ...first, data, next_cursor: '' };
        if (seen.has(result.next_cursor) || result.next_cursor === cursor) {
          throw new ApiError('The server repeated a release queue cursor.', 500, 'invalid_pagination_cursor');
        }
        seen.add(result.next_cursor);
        cursor = result.next_cursor;
      }
      throw new ApiError('The release work queue exceeded its browser pagination safety limit.', 500, 'pagination_limit');
    },

    /** Short alias for the read-only dependency-aware release queue. */
    releaseWorkQueue: (release: string, params: ReleaseWorkQueueParams = {}) =>
      getApi().getReleaseWorkQueue(release, params),

    listReleaseWorkQueue: (release: string, params: ReleaseWorkQueueParams = {}) =>
      getApi().getReleaseWorkQueue(release, params),

    getReleaseQueue: (release: string, params: ReleaseWorkQueueParams = {}) =>
      getApi().getReleaseWorkQueue(release, params)
  };
}
