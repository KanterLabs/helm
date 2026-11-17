import type { PullRequestLink, Task } from '../types';
import { key, request } from './client';

const base = (taskId: string) => `/tasks/${encodeURIComponent(taskId)}/pull-requests`;

export const pullRequestsApi = {
  listTaskPullRequests: (taskId: string, signal?: AbortSignal) =>
    request<{ items: PullRequestLink[] }>(base(taskId), { signal }),
  linkTaskPullRequest: (taskId: string, url: string, version: number) =>
    request<{ link: PullRequestLink; task: Task }>(base(taskId), {
      method: 'POST', body: { url }, ifMatch: version, idempotencyKey: key()
    }),
  unlinkTaskPullRequest: (taskId: string, linkId: string, version: number) =>
    request<{ task: Task }>(`${base(taskId)}/${encodeURIComponent(linkId)}`, {
      method: 'DELETE', ifMatch: version, idempotencyKey: key()
    })
};
