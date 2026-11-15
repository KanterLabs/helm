import type { Page, Route } from '@playwright/test';
import { installGithubMocks } from './githubFixture';
import { API, error, json, WEB, type Json } from './githubShared';

// Synthetic device-flow connection (contract C5) layered over the GitHub
// beta mocks. Every login, code and repository name here is invented.
export const LOGIN = 'synthetic-admin';
export const CODES = ['WDJB-MJHT', 'QXRT-7KPL', 'HMNV-3CZD', 'LBTY-9WQE'];
export const DEVICE_URL = 'https://github.com/login/device';
export const INSTALL_URL = 'https://github.com/apps/synthetic-helm/installations/new';
export const AUTHORIZATIONS_URL = 'https://github.com/settings/apps/authorizations';
export const A3_MESSAGE = 'Helm did not get a proper answer from its own server. Try again in a moment.';

export type Attempt = { id: string; state: string; user_code?: string; verification_uri: string; expires_at: string; message: string };
export type Connection = {
  mode: 'operator_token' | 'device_flow';
  signin_available: boolean;
  state: string;
  login?: string;
  connected_at?: string;
  install_url?: string;
  authorizations_url?: string;
  repositories_truncated?: boolean;
  attempt?: Attempt;
};

export type Failure = 'html502' | { status: number; code: string; message: string };

export type ConnectionMock = {
  connection: Connection | undefined;
  repositories: string[];
  /** Every request under /integrations/github, as "METHOD /path". */
  calls: string[];
  /** Status-document GETs answered or held, in order. */
  gets: () => number;
  /** Hold every status GET until release() (to observe overlap). */
  holdGets: boolean;
  release: () => Promise<void>;
  /** Fail the next request matching "METHOD /path" once. */
  failNext: (call: string, failure: Failure) => void;
  /** Pretend GitHub approved the current attempt. */
  approve: (repositories?: string[]) => void;
  /** Start a pending attempt as if another administrator had pressed Connect. */
  otherAdminConnects: () => void;
  set: (patch: Partial<Connection>) => void;
};

const now = () => Date.now();

export function baseConnection(state: string, extra: Partial<Connection> = {}): Connection {
  const connected = state === 'connected' || state === 'no_repositories';
  return {
    mode: 'device_flow', signin_available: true, state,
    ...(connected ? { login: LOGIN, connected_at: new Date(now() - 2 * 3_600_000).toISOString() } : {}),
    install_url: INSTALL_URL, authorizations_url: AUTHORIZATIONS_URL, repositories_truncated: false,
    ...extra
  };
}

export function pendingAttempt(index: number, extra: Partial<Attempt> = {}): Attempt {
  return {
    id: `attempt-synthetic-${index}`, state: 'pending', user_code: CODES[index % CODES.length],
    verification_uri: DEVICE_URL, expires_at: new Date(now() + 15 * 60_000).toISOString(), message: '', ...extra
  };
}

async function fail(route: Route, failure: Failure): Promise<void> {
  if (failure === 'html502') {
    return route.fulfill({ status: 502, contentType: 'text/html', body: '<!DOCTYPE html><html><head><title>502 Bad Gateway</title></head><body><h1>Bad gateway</h1><p>Synthetic proxy error page ray-id 0000synthetic</p></body></html>' });
  }
  return error(route, failure.status, failure.code, failure.message);
}

/**
 * Installs the shared GitHub mocks plus a stateful `/integrations/github*`
 * handler. `connection: undefined` imitates a server older than device flow.
 */
export async function installConnectionMocks(page: Page, connection: Connection | undefined, repositories: string[] = []): Promise<ConnectionMock> {
  await installGithubMocks(page);
  let attempts = 0;
  let held: Array<() => Promise<void>> = [];
  const failures = new Map<string, Failure>();
  const mock: ConnectionMock = {
    connection, repositories, calls: [], holdGets: false,
    gets: () => mock.calls.filter((call) => call === 'GET /integrations/github').length,
    release: async () => { const list = held; held = []; await Promise.all(list.map((run) => run())); },
    failNext: (call, failure) => { failures.set(call, failure); },
    approve: (repos = [WEB, API]) => {
      mock.repositories = repos;
      mock.set({ state: repos.length ? 'connected' : 'no_repositories', login: LOGIN, connected_at: new Date(now()).toISOString(), attempt: undefined });
    },
    otherAdminConnects: () => { attempts += 1; mock.set({ attempt: pendingAttempt(attempts, { user_code: undefined }) }); },
    set: (patch) => {
      const next = { ...(mock.connection || baseConnection('disconnected')), ...patch } as Json;
      Object.keys(next).forEach((key) => { if (next[key] === undefined) delete next[key]; });
      mock.connection = next as Connection;
    }
  };

  const documentBody = (checked = false): Json => {
    const conn = mock.connection;
    // operator_token and older servers answer exactly as before device flow.
    const legacy = !conn || conn.mode === 'operator_token';
    const ready = legacy || conn.state === 'connected';
    return {
      enabled: true,
      configured: ready || conn.state === 'no_repositories',
      state: ready ? 'connected' : 'not_configured',
      message: ready ? 'Synthetic: Helm can read the allowed repositories.' : '',
      allowed_repositories: legacy ? [WEB, API] : mock.repositories,
      checked_at: checked ? new Date(now()).toISOString() : '2026-11-14T09:00:00Z',
      ...(conn ? { connection: conn } : {})
    };
  };

  await page.route(/\/api\/v1\/integrations\/github(\/.*)?$/, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace('/api/v1', '');
    const call = `${request.method()} ${path}`;
    mock.calls.push(call);
    const failure = failures.get(call);
    if (failure) { failures.delete(call); return fail(route, failure); }
    const conn = mock.connection;
    if (call === 'GET /integrations/github') {
      // Snapshot now, answer later: a held answer reflects when it was asked.
      const body = documentBody();
      if (mock.holdGets) { held.push(() => json(route, body)); return; }
      return json(route, body);
    }
    if (call === 'POST /integrations/github/check') return json(route, documentBody(true));
    if (!conn || conn.mode === 'operator_token') return error(route, 409, 'github_operator_managed', 'Synthetic operator-managed refusal.');
    if (call === 'POST /integrations/github/connect') {
      if (!conn.signin_available) return error(route, 409, 'github_signin_unavailable', 'Synthetic sign-in unavailable.');
      if (!conn.attempt || conn.attempt.state !== 'pending' || !conn.attempt.user_code) {
        attempts += 1;
        mock.set({ attempt: pendingAttempt(attempts) });
      }
      return json(route, documentBody());
    }
    if (call === 'POST /integrations/github/connect/cancel') {
      const body = request.postDataJSON() as { attempt_id?: string };
      if (conn.attempt && conn.attempt.id === body?.attempt_id) mock.set({ attempt: undefined });
      return json(route, documentBody());
    }
    if (call === 'POST /integrations/github/disconnect') {
      mock.repositories = [];
      mock.set({ state: 'disconnected', login: undefined, connected_at: undefined, attempt: undefined });
      return json(route, documentBody());
    }
    return error(route, 404, 'mock_route_missing', `Unhandled ${call}`);
  });
  return mock;
}

/** Make `document.hidden` report the given value and fire visibilitychange. */
export async function setHidden(page: Page, hidden: boolean): Promise<void> {
  await page.evaluate((value) => {
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => value });
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => (value ? 'hidden' : 'visible') });
    document.dispatchEvent(new Event('visibilitychange'));
  }, hidden);
}

/** Leave Settings through in-app navigation, which unmounts the panel. */
export async function leaveSettings(page: Page, slug: string): Promise<void> {
  await page.evaluate((target) => {
    history.pushState({}, '', `/p/${target}`);
    dispatchEvent(new PopStateEvent('popstate'));
  }, slug);
}
