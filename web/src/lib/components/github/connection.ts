import { ApiError, type GithubConnection } from '../../types';

// Pure helpers for the device-flow GitHub connection panel (contract C6).
// Every sentence here is Helm's own: nothing from GitHub reaches the screen.

export type ConnectionView =
  | 'unavailable' | 'disconnected' | 'starting' | 'pending' | 'other_pending'
  | 'expired' | 'denied' | 'failed' | 'no_repositories' | 'connected' | 'reconnect_required';

export const POLL_MS = 3_000;
export const NOT_HELMS_ANSWER = 'Helm did not get a proper answer from its own server. Try again in a moment.';

/** Which panel to show. A pending attempt past its expiry reads as expired. */
export function connectionView(connection: GithubConnection, now: number): ConnectionView {
  const attempt = connection.attempt;
  const linked = connection.state === 'connected' || connection.state === 'no_repositories';
  if (!connection.signin_available && !linked) return 'unavailable';
  if (attempt?.state === 'pending') {
    const expires = Date.parse(attempt.expires_at);
    if (Number.isFinite(expires) && expires <= now) return 'expired';
    return attempt.user_code ? 'pending' : 'other_pending';
  }
  if (attempt && !linked && (attempt.state === 'expired' || attempt.state === 'denied' || attempt.state === 'failed')) return attempt.state;
  if (connection.state === 'connected') return 'connected';
  if (connection.state === 'no_repositories') return 'no_repositories';
  if (connection.state === 'reconnect_required') return 'reconnect_required';
  return 'disconnected';
}

export const polls = (view: ConnectionView) => view === 'pending' || view === 'other_pending';

/** The URL itself when its origin is exactly https://github.com, else ''. */
export function githubUrl(value: string | undefined | null): string {
  if (!value || !value.startsWith('https://github.com/')) return '';
  try {
    const url = new URL(value);
    return url.origin === 'https://github.com' && !url.username && !url.password ? url.href : '';
  } catch {
    return '';
  }
}

/** "14:05" for the time left; "0:00" once expired. */
export function timeLeft(expiresAt: string | undefined, now: number): string {
  const ms = Math.max(0, Date.parse(expiresAt || '') - now);
  if (!Number.isFinite(ms)) return '';
  const seconds = Math.ceil(ms / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

export function secondsLeft(expiresAt: string | undefined, now: number): number {
  const ms = Date.parse(expiresAt || '') - now;
  return Number.isFinite(ms) ? Math.ceil(ms / 1000) : 0;
}

/**
 * A plain sentence for a failed request. A failure without Helm's own error
 * code (a proxy's page, a network failure) is never blamed on GitHub.
 */
export function plainError(failure: unknown, fallback: string): string {
  if (!(failure instanceof ApiError) || failure.code === 'request_failed') return NOT_HELMS_ANSWER;
  switch (failure.code) {
    case 'offline_read_only': return 'Helm is offline, so it cannot change the GitHub connection. Reconnect, then try again.';
    case 'github_operator_managed': return 'An operator manages GitHub for this Helm with a token file, so sign-in here is off. Reload the page to see the current setup.';
    case 'github_signin_unavailable': return 'GitHub sign-in is not set up on this Helm. Ask your operator to set it up, then reload this page.';
  }
  if (failure.status === 424) return 'Helm could not reach GitHub. Wait a minute, then try again.';
  if (failure.status === 401) return 'Your Helm session has ended. Sign in again, then try again.';
  if (failure.status === 403) return 'Only a Helm administrator can change the GitHub connection.';
  if (failure.status === 409) return 'The GitHub connection changed in the meantime. The panel now shows the latest state; try again if you still need to.';
  if (failure.status === 429) return 'Too many tries in a short time. Wait a minute, then try again.';
  return fallback;
}

/** Spoken when the panel moves to a new view (never on first load). */
export function announcement(view: ConnectionView, connection: GithubConnection, cause: string): string {
  switch (view) {
    case 'starting': return 'Getting a code from GitHub.';
    case 'pending': return `Code ready: ${connection.attempt?.user_code || ''}. Enter it on GitHub.`;
    case 'other_pending': return 'Another administrator is connecting GitHub.';
    case 'expired': return 'The code expired. Press Try again for a new one.';
    case 'denied': return 'GitHub sign-in was declined. Helm is not connected.';
    case 'failed': return 'GitHub sign-in did not finish. Helm is not connected.';
    case 'no_repositories': return 'Signed in to GitHub. Next, install Helm on your repositories.';
    case 'connected': return `GitHub is connected as @${connection.login || 'your account'}.`;
    case 'reconnect_required': return 'GitHub sign-in needs renewing. Press Reconnect GitHub.';
    case 'unavailable': return 'GitHub sign-in is not set up on this Helm.';
    case 'disconnected': return cause === 'cancel' ? 'Sign-in cancelled.' : 'GitHub is disconnected.';
  }
}

/** Health of a connected integration after its last check, in plain words. */
export function healthNote(state: string): string {
  switch (state) {
    case 'permission_required': return 'Helm cannot read one or more repositories. Use Choose repositories on GitHub to give it access, then test again.';
    case 'unavailable': return 'Helm could not reach GitHub just now. Other Helm work is unaffected; test again in a few minutes.';
    case 'rate_limited': return 'GitHub is limiting how often Helm can ask. Helm slows down by itself; test again later.';
    default: return '';
  }
}

/** Header badge for the device-flow panel. */
export function badge(view: ConnectionView, integrationState: string): { label: string; state: string } {
  switch (view) {
    case 'unavailable': return { label: 'Not configured', state: 'not_configured' };
    case 'starting': case 'pending': case 'other_pending': return { label: 'Waiting for GitHub', state: 'pending' };
    case 'no_repositories': return { label: 'Needs repositories', state: 'permission_required' };
    case 'reconnect_required': return { label: 'Reconnect needed', state: 'permission_required' };
    case 'connected':
      if (integrationState === 'unavailable') return { label: 'Unavailable', state: 'unavailable' };
      if (integrationState === 'rate_limited') return { label: 'Rate limited', state: 'rate_limited' };
      if (integrationState === 'permission_required') return { label: 'Permission required', state: 'permission_required' };
      return { label: 'Connected', state: 'connected' };
    default: return { label: 'Not connected', state: 'not_configured' };
  }
}
