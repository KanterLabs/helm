import type {
  BoardDescriptor,
  Collection,
  Column,
  Label,
  PortableArchive,
  PortableImportReport,
  Project
} from '../types';
import { collectPages, collectionFrom, key, pathWithQuery, request } from './client';

export const projectsApi = {
  listProjects: (params: { cursor?: string; limit?: number } = {}) =>
    request<Collection<Project>>(
      pathWithQuery('/projects', { cursor: params.cursor, limit: params.limit ?? 100 })
    ).then(collectionFrom),

  listAllProjects: (params: { includeArchived?: boolean } = {}) => collectPages((cursor) =>
    request<Collection<Project>>(pathWithQuery('/projects', { cursor, limit: 200, archived: params.includeArchived ? true : undefined })).then(collectionFrom)
  ),

  createProject: (input: { key: string; name: string; description?: string; color?: string; favorite?: boolean; checklist_completion_policy?: Project['checklist_completion_policy'] }, idempotencyKey = key()) =>
    request<Project>('/projects', { method: 'POST', body: input, idempotencyKey }),

  getProject: (project: string) => request<Project>(`/projects/${encodeURIComponent(project)}`),

  patchProject: (project: string, input: Partial<Pick<Project, 'name' | 'description' | 'color' | 'favorite' | 'checklist_completion_policy'>> & { archived?: boolean }, version?: number) =>
    request<Project>(`/projects/${encodeURIComponent(project)}`, { method: 'PATCH', body: input, ifMatch: version, idempotencyKey: key() }),

  exportProject: (project?: string) =>
    request<PortableArchive>(pathWithQuery(project ? `/projects/${encodeURIComponent(project)}/export` : '/export', { version: 3 })),

  importPortable: (archive: PortableArchive | unknown, options: { targetProject?: string; conflict?: 'remap' | 'fail'; dryRun?: boolean } = {}) =>
    request<PortableImportReport>('/import', {
      method: 'POST',
      body: {
        archive,
        target_project: options.targetProject,
        conflict: options.conflict,
        dry_run: options.dryRun
      },
      idempotencyKey: options.dryRun ? undefined : key()
    }),

  importTrello: (board: unknown, options: { targetProject?: string; conflict?: 'remap' | 'fail'; dryRun?: boolean } = {}) =>
    request<PortableImportReport>(pathWithQuery('/import/trello', { target_project: options.targetProject, conflict: options.conflict, dry_run: options.dryRun }), {
      method: 'POST',
      body: board,
      idempotencyKey: options.dryRun ? undefined : key()
    }),

  listBoards: (project: string) =>
    request<{ data: BoardDescriptor[]; next_cursor?: string | null; multi_board?: Record<string, unknown> }>(`/projects/${encodeURIComponent(project)}/boards`),

  listColumns: (project: string, params: { cursor?: string; limit?: number } = {}) =>
    request<Collection<Column> | Column[]>(
      pathWithQuery(`/projects/${encodeURIComponent(project)}/columns`, { cursor: params.cursor, limit: params.limit })
    ).then(collectionFrom),

  listAllColumns: (project: string, params: { includeArchived?: boolean } = {}) => collectPages((cursor) =>
    request<Collection<Column>>(
      pathWithQuery(`/projects/${encodeURIComponent(project)}/columns`, { cursor, limit: 200, archived: params.includeArchived ? true : undefined })
    ).then(collectionFrom)
  ),

  createColumn: (project: string, input: { name: string; semantic_state: Column['semantic_state']; position?: number }) =>
    request<Column>(`/projects/${encodeURIComponent(project)}/columns`, {
      method: 'POST',
      body: input,
      idempotencyKey: key()
    }),

  patchColumn: (column: string, input: Partial<Pick<Column, 'name' | 'semantic_state' | 'position'>> & { archived?: boolean }, version?: number) =>
    request<Column>(`/columns/${encodeURIComponent(column)}`, {
      method: 'PATCH',
      body: input,
      ifMatch: version,
      idempotencyKey: key()
    }),

  /** An embedded user guide (Markdown) for the in-app help drawer. */
  getHelpDocument: (page: string) => request<string>(`/docs/${encodeURIComponent(page)}`),

  listLabels: (project: string, params: { cursor?: string; limit?: number } = {}) =>
    request<Collection<Label> | Label[]>(
      pathWithQuery(`/projects/${encodeURIComponent(project)}/labels`, { cursor: params.cursor, limit: params.limit })
    ).then(collectionFrom),

  listAllLabels: (project: string) => collectPages((cursor) =>
    request<Collection<Label>>(
      pathWithQuery(`/projects/${encodeURIComponent(project)}/labels`, { cursor, limit: 200 })
    ).then(collectionFrom)
  ),

  createLabel: (project: string, input: { name: string; color?: string }) =>
    request<Label>(`/projects/${encodeURIComponent(project)}/labels`, {
      method: 'POST',
      body: input,
      idempotencyKey: key()
    }),

  deleteLabel: (label: string) =>
    request<void>(`/labels/${encodeURIComponent(label)}`, { method: 'DELETE', idempotencyKey: key() })
};
