import type { Agent, ApiToken, Project, Scope } from './types';

export const DAY = 86_400_000;
export const tokenPresets: Record<string, { label: string; scopes: Scope[]; description: string }> = {
  reader: { label: 'Read only', scopes: ['projects:read', 'tasks:read'], description: 'Read project and task information.' },
  worker: { label: 'Task worker', scopes: ['projects:read', 'tasks:read', 'tasks:write', 'tasks:claim', 'events:read'], description: 'Read and update tasks, claim work and read events.' },
  maintainer: { label: 'Maintainer', scopes: ['projects:read', 'projects:write', 'tasks:read', 'tasks:write', 'tasks:claim', 'events:read'], description: 'Task worker access plus project changes.' }
};
export const tokenScopes: Array<{ scope: Scope; description: string }> = [
  { scope: 'projects:read', description: 'Read project information' },
  { scope: 'projects:write', description: 'Create and change projects' },
  { scope: 'tasks:read', description: 'Read tasks and their details' },
  { scope: 'tasks:write', description: 'Create and change tasks' },
  { scope: 'tasks:claim', description: 'Claim work and publish progress' },
  { scope: 'events:read', description: 'Read activity events' },
  { scope: 'notifications:read', description: 'Read coordination notifications' },
  { scope: 'notifications:write', description: 'Update notification preferences and state' }
];

export function tokenPreset(scopes: readonly string[]): string {
  const set = new Set(scopes);
  return Object.keys(tokenPresets).find((key) => tokenPresets[key].scopes.length === set.size && tokenPresets[key].scopes.every((scope) => set.has(scope))) || 'custom';
}
export function accessLabel(token: ApiToken): string {
  return tokenPresets[tokenPreset(token.scopes)]?.label || `Custom · ${token.scopes.length} scopes`;
}
export function tokenStatus(token: ApiToken, now = Date.now()): string {
  if (token.expires_at == null) return 'Active';
  const expires = Date.parse(token.expires_at);
  if (!Number.isFinite(expires)) return 'Unknown';
  if (expires <= now) return 'Expired';
  return expires - now <= 14 * DAY ? 'Expiring soon' : 'Active';
}
export function exactTime(value?: string | null): string {
  if (!value) return 'Not recorded';
  const time = Date.parse(value);
  return Number.isFinite(time) ? new Date(time).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }) : 'Unknown date';
}
export function usedAgo(value?: string | null, now = Date.now()): string {
  if (!value) return 'Never';
  const elapsed = now - Date.parse(value);
  if (!Number.isFinite(elapsed)) return 'Unknown';
  if (elapsed < 60_000) return 'Just now';
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} minutes ago`;
  if (elapsed < DAY) return `${Math.floor(elapsed / 3_600_000)} hours ago`;
  return `${Math.floor(elapsed / DAY)} days ago`;
}
export function expiryLabel(token: ApiToken, now = Date.now()): string {
  if (token.expires_at == null) return 'No expiry';
  const delta = Date.parse(token.expires_at) - now;
  if (!Number.isFinite(delta)) return 'Unknown expiry';
  if (delta <= 0) return `Expired ${exactTime(token.expires_at)}`;
  return delta < DAY ? 'Expires in less than a day' : `Expires in ${Math.ceil(delta / DAY)} days`;
}
export function effectiveProjectIds(agent: Agent, token: ApiToken): string[] {
  if (!agent.project_ids?.length) return token.project_ids || [];
  return token.project_ids?.length ? token.project_ids.filter((id) => agent.project_ids!.includes(id)) : agent.project_ids;
}
export function projectLabels(ids: readonly string[], projects: readonly Project[]): string {
  return ids.map((id) => projects.find((project) => project.id === id)?.name || `Unavailable project (${id.slice(0, 8)})`).join(', ');
}
export function tokenProjectLabel(agent: Agent, token: ApiToken, projects: readonly Project[]): string {
  const effective = effectiveProjectIds(agent, token);
  if (!token.project_ids?.length) return `All agent projects${agent.project_ids?.length ? ` · ${projectLabels(effective, projects)}` : ' · includes future projects'}`;
  return effective.length ? projectLabels(effective, projects) : 'No effective project access';
}
export function sortTokens(tokens: readonly ApiToken[], now = Date.now()): ApiToken[] {
  const rank: Record<string, number> = { 'Expiring soon': 0, Active: 1, Expired: 2, Unknown: 3 };
  return [...tokens].sort((a, b) => rank[tokenStatus(a, now)] - rank[tokenStatus(b, now)] || (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0) || a.id.localeCompare(b.id));
}
