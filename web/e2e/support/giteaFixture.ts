import { expect, type Locator, type Page, type Route, type TestInfo } from '@playwright/test';
import { installGithubMocks } from './githubFixture';
import { json, type Json } from './githubShared';

// Synthetic Gitea connection API (contract C5 in docs/GITEA_OAUTH_PLAN.md)
// for the Settings panel specs. Every host, login, ID and repository here is
// invented. The probe answers by host name, so a test picks a behaviour by
// typing an address: see `probeFor`.

export const BASE = 'https://gitea.example.com';
export const CLIENT_ID = '3f2b9c1e-7a4d-4e8b-9c0f-1a2b3c4d5e6f';
// Long on purpose: it must wrap at 320 px.
export const REDIRECT_URI = 'https://helm-workspace-for-the-platform-team.example.org/api/v1/integrations/gitea/oauth/callback';
export const DRAFT_KEY = 'helm.gitea.draft';
const stamp = '2026-11-12T09:00:00Z';

export type GiteaStatus = Json;
export type Recorded = { method: string; path: string; body: Json | undefined; idempotencyKey: string | null };

const server = (base: string, product = 'gitea', version = '28.3.0', insecure = false) => ({ base_url: base, product, version, insecure_http: insecure });

export function disconnectedStatus(extra: Json = {}): GiteaStatus {
  return {
    available: true, unavailable_reason: '', redirect_uri: REDIRECT_URI, state: 'disconnected', server: null,
    client_id: '', account: null, access: 'unknown', repository_count: 0, connected_at: null, checked_at: null,
    check: { state: 'ok', code: '' }, register_url: '', revoke_url: '', ...extra
  };
}

export function connectedStatus(extra: Json = {}): GiteaStatus {
  const base = (extra.server?.base_url as string) || BASE;
  return disconnectedStatus({
    state: 'connected', server: server(base), client_id: CLIENT_ID, account: { login: 'helm-reader', id: 7 },
    access: 'scoped', repository_count: 12, connected_at: stamp, checked_at: stamp, check: { state: 'ok', code: '' },
    register_url: `${base}/user/settings/applications`, revoke_url: `${base}/user/settings/applications`, ...extra
  });
}

export const fail = (route: Route, status: number, code: string, details: Json = {}) =>
  json(route, { error: { code, message: `Upstream said: synthetic-leak-${code}`, details } }, status);

/** The settled base address the mock "server" works out from any input. */
export function settle(input: string): string {
  const value = input.trim();
  const url = new URL(/^[a-z]+:\/\//i.test(value) ? value : `https://${value}`);
  return `${url.protocol}//${url.host}`.toLowerCase();
}

type ProbeResult = { status: number; body: Json };

/** Host-keyed probe behaviour. */
export function probeFor(body: Json): ProbeResult {
  const raw = String(body.base_url || '');
  const failed = (status: number, code: string, details: Json = {}) => ({ status, body: { error: { code, message: `Upstream said: synthetic-leak-${code}`, details } } });
  if (!raw.trim() || /\s/.test(raw.trim()) || !/[.:]|localhost/.test(raw)) return failed(400, 'invalid_server_url');
  const base = settle(raw);
  const host = new URL(base).hostname;
  if (base.startsWith('http:') && !body.allow_insecure_http) return failed(400, 'insecure_http');
  if (host.startsWith('unreachable')) return failed(502, 'server_unreachable');
  if (host.startsWith('badcert')) return failed(502, 'tls_error');
  if (host.startsWith('notaforge')) return failed(422, 'not_a_forge');
  if (host.startsWith('moved')) return failed(422, 'issuer_mismatch', { issuer: 'https://git.example.com' });
  if (host.startsWith('busy')) return failed(409, 'already_connected');
  if (host.startsWith('broken')) return failed(500, 'mystery_upstream_failure');
  const forgejo = host.startsWith('forgejo');
  return {
    status: 200,
    body: {
      server: server(base, forgejo ? 'forgejo' : 'gitea', forgejo ? '11.0.1+gitea-1.22.0' : '28.3.0', base.startsWith('http:')),
      redirect_uri: REDIRECT_URI,
      register_url: `${base}/user/settings/applications`
    }
  };
}

export function repositories(count: number): Json[] {
  const names = ['platform/helm-web', 'platform/helm-api', 'platform/infra-terraform-modules-shared-with-everyone', 'tools/release-notes', 'tools/ci-images', 'docs/handbook', 'docs/runbooks', 'data/etl', 'data/reports', 'mobile/app', 'mobile/design', 'archive/legacy-portal', 'labs/experiments'];
  return Array.from({ length: count }, (_, index) => ({ full_name: names[index % names.length] + (index >= names.length ? `-${index}` : ''), private: index % 3 === 0, archived: index === 11 }));
}

export type GiteaMock = {
  status: GiteaStatus;
  requests: Recorded[];
  /** Override a route's answer; return undefined to use the default. */
  probe?: (body: Json) => ProbeResult | Promise<ProbeResult> | undefined;
  start?: (body: Json) => ProbeResult | undefined | Promise<ProbeResult | undefined>;
  /** While set, the status read waits for it (to show the skeleton). */
  statusGate?: Promise<void>;
  check?: () => GiteaStatus;
  repositories: { list: Json[]; truncated: boolean; fail?: boolean };
  statusFails: number;
  count: (method: string, path: string) => number;
};

/**
 * Installs the shared admin workspace mocks, then the Gitea routes on top.
 * A synthetic forge page answers any navigation to an example.* forge host.
 */
export async function installGiteaMocks(page: Page, initial: GiteaStatus = disconnectedStatus()): Promise<GiteaMock> {
  await installGithubMocks(page);
  const mock: GiteaMock = {
    status: initial,
    requests: [],
    repositories: { list: repositories(12), truncated: false },
    statusFails: 0,
    count: (method, path) => mock.requests.filter((item) => item.method === method && item.path === path).length
  };
  await page.route('**/api/v1/integrations/gitea**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace('/api/v1/integrations/gitea', '') || '/';
    const method = request.method();
    const body = request.postData() ? request.postDataJSON() as Json : undefined;
    mock.requests.push({ method, path, body, idempotencyKey: request.headers()['idempotency-key'] || null });
    if (method === 'GET' && path === '/') {
      if (mock.statusGate) await mock.statusGate;
      if (mock.statusFails > 0) { mock.statusFails -= 1; return fail(route, 500, 'internal'); }
      return json(route, mock.status);
    }
    if (method === 'POST' && path === '/probe') {
      const result = (await mock.probe?.(body || {})) || probeFor(body || {});
      return json(route, result.body, result.status);
    }
    if (method === 'POST' && path === '/oauth/start') {
      const custom = await mock.start?.(body || {});
      if (custom) return json(route, custom.body, custom.status);
      const base = body?.base_url ? settle(String(body.base_url)) : mock.status.server?.base_url;
      return json(route, { authorize_url: `${base}/login/oauth/authorize?client_id=${encodeURIComponent(body?.client_id || mock.status.client_id)}&response_type=code&state=synthetic-state` });
    }
    if (method === 'POST' && path === '/check') {
      mock.status = mock.check ? mock.check() : { ...mock.status, checked_at: new Date().toISOString() };
      return json(route, mock.status);
    }
    if (method === 'GET' && path === '/repositories') {
      if (mock.repositories.fail) return fail(route, 502, 'server_unreachable');
      return json(route, { repositories: mock.repositories.list, truncated: mock.repositories.truncated });
    }
    if (method === 'POST' && path === '/disconnect') {
      mock.status = disconnectedStatus();
      return json(route, mock.status);
    }
    return fail(route, 404, 'mock_route_missing');
  });
  await page.route(/^https:\/\/[a-z0-9.-]*example\.(com|net|org)\//, (route) =>
    route.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html><title>Synthetic forge</title><h1>Synthetic forge consent page</h1>' }));
  return mock;
}

/** Overrides the shared admin session with a signed-in workspace member. */
export async function signInAsMember(page: Page): Promise<void> {
  await page.route('**/api/v1/auth/status', (route) => {
    const actor = { id: 'human-member', kind: 'human', name: 'Synthetic member', workspace_role: 'member', admin: false };
    return json(route, { mode: 'local', configured: true, setup_required: false, authenticated: true, actor, user: actor });
  });
}

export const panel = (page: Page): Locator => page.locator('[data-gitea-settings]');
export const addressField = (page: Page): Locator => panel(page).getByRole('textbox', { name: /Server address/ });
export const clientField = (page: Page): Locator => panel(page).getByRole('textbox', { name: /Client ID/ });
export const liveRegion = (page: Page): Locator => panel(page).locator('[data-gitea-live]');

export async function openSettings(page: Page, query = ''): Promise<void> {
  await page.goto(`/settings${query}`);
  await expect(panel(page)).toBeVisible();
  await expect(panel(page).locator('[data-gitea-loading]')).toHaveCount(0);
}

const frame = (page: Page) => page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));

/**
 * Screenshots the panel at 1280 px and 390 px into the run's output, then
 * proves the page has no horizontal overflow at 320 px.
 */
export async function snap(page: Page, testInfo: TestInfo, name: string): Promise<void> {
  const original = page.viewportSize() || { width: 1280, height: 900 };
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await frame(page);
    const file = testInfo.outputPath(`gitea-${name}-${width}.png`);
    // The sticky top bar and fixed mobile nav are app chrome, not the panel.
    await panel(page).screenshot({ path: file, animations: 'disabled', style: '.topbar, .mobile-nav, .toast-stack { visibility: hidden !important; }' });
    await testInfo.attach(`${name}-${width}`, { path: file, contentType: 'image/png' });
  }
  await page.setViewportSize({ width: 320, height: 900 });
  await frame(page);
  const overflow = await page.evaluate(() => {
    const root = document.documentElement;
    const section = document.querySelector('[data-gitea-settings]') as HTMLElement | null;
    const wide = section ? Array.from(section.querySelectorAll<HTMLElement>('*')).filter((node) => node.getBoundingClientRect().right > root.clientWidth + 0.5).map((node) => node.tagName + '.' + node.className) : [];
    return { page: root.scrollWidth - root.clientWidth, wide: wide.slice(0, 5) };
  });
  expect(overflow, `${name}: no horizontal overflow at 320 px`).toEqual({ page: 0, wide: [] });
  await page.setViewportSize(original);
  await frame(page);
}

/** No request on these routes may carry an Idempotency-Key. */
export function expectNoIdempotencyKeys(mock: GiteaMock): void {
  expect(mock.requests.filter((item) => item.idempotencyKey)).toEqual([]);
}
