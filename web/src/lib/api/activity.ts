import type {
  ActivityEvent,
  AgentNote,
  Collection,
  Comment,
  Notification,
  NotificationPreferences,
  TaskTimelineCollection,
  TaskTimelineKind,
  Watch
} from '../types';
import { collectPages, collectionFrom, key, pathWithQuery, request } from './client';

export const activityApi = {
  listComments: (task: string, params: { cursor?: string; limit?: number } = {}) =>
    request<Collection<Comment> | Comment[]>(
      pathWithQuery(`/tasks/${encodeURIComponent(task)}/comments`, { cursor: params.cursor, limit: params.limit })
    ).then(collectionFrom),

  listAllComments: (task: string) => collectPages((cursor) =>
    request<Collection<Comment>>(
      pathWithQuery(`/tasks/${encodeURIComponent(task)}/comments`, { cursor, limit: 200 })
    ).then(collectionFrom)
  ),

  getComment: (task: string, comment: string) =>
    request<Comment>(`/tasks/${encodeURIComponent(task)}/comments/${encodeURIComponent(comment)}`),

  postComment: (task: string, body: string) =>
    request<Comment>(`/tasks/${encodeURIComponent(task)}/comments`, {
      method: 'POST',
      body: { body },
      idempotencyKey: key()
    }),

  patchComment: (task: string, comment: string, body: string, version: number) =>
    request<Comment>(`/tasks/${encodeURIComponent(task)}/comments/${encodeURIComponent(comment)}`, {
      method: 'PATCH',
      body: { body },
      ifMatch: version,
      idempotencyKey: key()
    }),

  deleteComment: (task: string, comment: string, version: number) =>
    request<void>(`/tasks/${encodeURIComponent(task)}/comments/${encodeURIComponent(comment)}`, {
      method: 'DELETE',
      ifMatch: version,
      idempotencyKey: key()
    }),

  listAgentNotes: (task: string, includeResolved = false) =>
    request<Collection<AgentNote>>(
      pathWithQuery(`/tasks/${encodeURIComponent(task)}/agent-notes`, { include_resolved: includeResolved || undefined })
    ).then(collectionFrom),

  updateAgentNote: (task: string, note: string, version: number, input: Pick<AgentNote, 'category' | 'body' | 'evidence'>) =>
    request<AgentNote>(`/tasks/${encodeURIComponent(task)}/agent-notes/${encodeURIComponent(note)}`, {
      method: 'PATCH', body: input, ifMatch: version, idempotencyKey: key()
    }),

  resolveAgentNote: (task: string, note: string, version: number) =>
    request<void>(`/tasks/${encodeURIComponent(task)}/agent-notes/${encodeURIComponent(note)}`, {
      method: 'DELETE', ifMatch: version, idempotencyKey: key()
    }),

  listTaskTimeline: (task: string, params: { before?: string; limit?: number; kind?: TaskTimelineKind } = {}) =>
    request<TaskTimelineCollection>(
      pathWithQuery(`/tasks/${encodeURIComponent(task)}/timeline`, {
        before: params.before,
        limit: params.limit ?? 50,
        kind: params.kind
      })
    ),

  listProjectTimeline: (project: string, params: { before?: string; limit?: number; kind?: TaskTimelineKind } = {}) =>
    request<TaskTimelineCollection>(
      pathWithQuery(`/projects/${encodeURIComponent(project)}/timeline`, {
        before: params.before,
        limit: params.limit ?? 50,
        kind: params.kind
      })
    ),

  listNotifications: (params: { unread?: boolean; cursor?: string; limit?: number } = {}) =>
    request<Collection<Notification> | Notification[]>(
      pathWithQuery('/notifications', {
        unread: params.unread,
        cursor: params.cursor,
        limit: params.limit ?? 25
      })
    ).then(collectionFrom),

  markNotificationRead: (notification: string, read = true) =>
    request<Notification>('/notifications/' + encodeURIComponent(notification), {
      method: 'PATCH',
      body: { read },
      idempotencyKey: key()
    }),

  markAllNotificationsRead: () =>
    request<{ marked_read: number }>('/notifications', {
      method: 'POST',
      body: { all: true },
      idempotencyKey: key()
    }),

  listWatches: (params: { project?: string; task?: string } = {}) =>
    request<Collection<Watch> | Watch[]>(pathWithQuery('/watches', { project: params.project, task: params.task })).then(collectionFrom),

  createWatch: (input: { project_id?: string; task_id?: string }) =>
    request<Watch>('/watches', { method: 'POST', body: input, idempotencyKey: key() }),

  deleteWatch: (watch: string) =>
    request<void>('/watches/' + encodeURIComponent(watch), { method: 'DELETE', idempotencyKey: key() }),

  getNotificationPreferences: () => request<NotificationPreferences>('/notification-preferences'),

  patchNotificationPreferences: (input: Partial<Omit<NotificationPreferences, 'actor_id' | 'updated_at'>>) =>
    request<NotificationPreferences>('/notification-preferences', { method: 'PATCH', body: input, idempotencyKey: key() }),

  listEvents: (params: { after?: number | string; project?: string } = {}) =>
    request<Collection<ActivityEvent> | ActivityEvent[]>(
      pathWithQuery('/events', { after: params.after, project: params.project })
    ).then(collectionFrom)
};
