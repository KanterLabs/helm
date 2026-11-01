import type { Actor, AuthStatus, Project, SessionDescriptor, WorkspaceRole } from '../../types';

// Client mirror of the server's admission rules. The server remains the
// authority; these only decide what to offer and what to fetch.
//
// - Development auth-disabled mode is a server-side bypass: always admitted
//   and administrative, including legacy fixtures without workspace_role.
// - Everywhere else a human without workspace_role is a verified identity
//   awaiting admission, regardless of any stale legacy `admin` bit.

export function isAdmissionPending(status: AuthStatus | null, user: Actor | null): boolean {
  if (!user || status?.mode === 'disabled') return false;
  return user.kind === 'human' && !user.workspace_role;
}

export function isWorkspaceAdmin(status: AuthStatus | null, user: Actor | null): boolean {
  if (!user || user.kind !== 'human' || user.disabled_at) return false;
  if (status?.mode === 'disabled') return true;
  return user.workspace_role === 'owner' || user.workspace_role === 'admin';
}

/** Local passwords and cookie sessions only exist in local auth mode. */
export function usesLocalCredentials(status: AuthStatus | null): boolean {
  return status?.mode === 'local';
}

export const roleLabels: Record<WorkspaceRole, string> = { owner: 'Owner', admin: 'Administrator', member: 'Member' };

export function roleLabel(actor: Pick<Actor, 'workspace_role'>): string {
  return actor.workspace_role ? roleLabels[actor.workspace_role] : 'Pending';
}

export function isAdminRole(role: WorkspaceRole | undefined): boolean {
  return role === 'owner' || role === 'admin';
}

export function accessSummary(actor: Pick<Actor, 'workspace_role' | 'project_ids'>): string {
  if (!actor.workspace_role) return 'No access until admitted';
  if (isAdminRole(actor.workspace_role)) return 'All projects';
  const count = actor.project_ids?.length ?? 0;
  if (!count) return 'No projects';
  return count === 1 ? '1 project' : `${count} projects`;
}

export function projectLabel(project: Project): string {
  const base = project.system_kind === 'tickets' ? `${project.name} (ticket queue)` : project.name;
  return project.archived_at ? `${base} (archived)` : base;
}

/** Same members compared as sets; order is not meaningful. */
export function sameIds(a: readonly string[], b: readonly string[]): boolean {
  if (a.length !== b.length) return false;
  const set = new Set(a);
  return b.every((id) => set.has(id));
}

export function minimumPasswordError(password: string, confirmation: string): string {
  if (password.length < 12) return 'Use at least 12 characters.';
  if (password !== confirmation) return 'The new passwords do not match.';
  return '';
}

export type SessionState = 'active' | 'revoked' | 'expired';

export function sessionState(session: SessionDescriptor, now = Date.now()): SessionState {
  if (session.revoked_at) return 'revoked';
  return Date.parse(session.expires_at) <= now ? 'expired' : 'active';
}

/** Newest activity first; ended sessions after active ones. */
export function sortSessions(sessions: SessionDescriptor[], now = Date.now()): SessionDescriptor[] {
  return [...sessions].sort((a, b) => {
    const activeA = sessionState(a, now) === 'active' ? 0 : 1;
    const activeB = sessionState(b, now) === 'active' ? 0 : 1;
    return activeA - activeB || Date.parse(b.last_seen_at) - Date.parse(a.last_seen_at);
  });
}

export function formatMoment(value?: string | null): string {
  if (!value) return 'Never';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return 'Unknown';
  return date.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

const browsers: Array<[RegExp, string]> = [
  [/HeadlessChrome\/(\d+)/, 'Headless Chrome'], [/Edg\/(\d+)/, 'Edge'], [/OPR\/(\d+)/, 'Opera'],
  [/Firefox\/(\d+)/, 'Firefox'], [/Chrome\/(\d+)/, 'Chrome'], [/Version\/(\d+)[^ ]* .*Safari\//, 'Safari']
];
const systems: Array<[RegExp, string]> = [
  [/iPhone|iPad/, 'iOS'], [/Android/, 'Android'], [/CrOS/, 'ChromeOS'], [/Windows/, 'Windows'], [/Mac OS X/, 'macOS'], [/Linux/, 'Linux']
];

/**
 * Readable device text from the server-recorded user agent, e.g. "Chrome 151
 * on Windows". Unrecognized agents are shown verbatim; the raw value stays
 * available through rawDevice for tooltips.
 */
export function deviceLabel(session: SessionDescriptor): string {
  const raw = rawDevice(session);
  const browser = browsers.find(([pattern]) => pattern.test(raw));
  if (!browser) return raw;
  const version = raw.match(browser[0])?.[1];
  const system = systems.find(([pattern]) => pattern.test(raw))?.[1];
  return `${browser[1]}${version ? ` ${version}` : ''}${system ? ` on ${system}` : ''}`;
}

export function rawDevice(session: SessionDescriptor): string {
  return session.user_agent?.trim() || 'Unknown browser';
}
