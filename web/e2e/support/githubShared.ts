import type { Route } from '@playwright/test';

// Shared synthetic constants and responders for the GitHub beta UI fixtures.
// Every repository, SHA, title and message here is invented test data.

export const project = {
  id: 'project-gh', key: 'GHB', slug: 'github-beta', name: 'GitHub Beta Demo',
  description: 'Synthetic board for GitHub beta UI checks.', color: '#6d5efc', favorite: true
};

export const columns = [
  { id: 'col-backlog', project_id: project.id, name: 'Backlog', semantic_state: 'backlog', position: 0 },
  { id: 'col-ready', project_id: project.id, name: 'Ready', semantic_state: 'ready', position: 1 },
  { id: 'col-active', project_id: project.id, name: 'In progress', semantic_state: 'active', position: 2 },
  { id: 'col-blocked', project_id: project.id, name: 'Blocked', semantic_state: 'blocked', position: 3 },
  { id: 'col-done', project_id: project.id, name: 'Done', semantic_state: 'completed', position: 4 }
];

export type Json = Record<string, any>;
export const stamp = '2026-10-26T10:00:00Z';
export const WEB = 'demo-org/helm-web';
export const API = 'demo-org/helm-api';
export const SHA_WEB = '9f3c2e1aa0b1c2d3e4f5061728394a5b6c7d8e9f';
export const SHA_API = '4b7a0d9bb0c1d2e3f405162738495a6b7c8d9e0f';

export function json(route: Route, body: unknown, status = 200): Promise<void> {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}
export const error = (route: Route, status: number, code: string, message: string, details: Json = {}) =>
  json(route, { error: { code, message, details } }, status);
