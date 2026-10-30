import { expect, type Browser, type TestInfo } from '@playwright/test';
import {
  attachCapture, attachScreenshot, baseURL, confirm, cookieWorks, createMemberViaApi, createProject,
  isPath, memberRow, membersRegion, mutationHeaders, openEditor, owner, recordContextCredentials, runId, signedInContext, syntheticPassword
} from './membershipFixture';
import { pendingActor } from './membershipSyntheticStates';

// Parent review regressions: use real membership/session mutations, with
// explicitly injected transport failures and delayed responses. No secrets
// are attached and password assertions never print field values.
export async function selfDemotionReconciles(browser: Browser, info: TestInfo, failStatus: boolean) {
  const root = await signedInContext(browser, owner.email, owner.password);
  const project = await createProject(root, 'PrivateReview');
  const id = runId();
  const person = { name: `Review Admin ${id}`, email: `review.${id}@example.test`, password: syntheticPassword(id), project_ids: [] };
  const member = await createMemberViaApi(root, person);
  const promoted = await root.request.patch(`/api/v1/members/${member.id}`, { headers: mutationHeaders(), data: { role: 'admin' } });
  expect(promoted.status()).toBe(200);
  const context = await signedInContext(browser, person.email, person.password);
  const page = await context.newPage();
  await page.goto('/settings');
  const nav = page.getByRole('navigation', { name: 'Primary navigation' });
  await expect(nav.getByText(project.name, { exact: true })).toBeVisible();
  const editor = await openEditor(page, person.name, person.email);
  await editor.getByLabel('Workspace role').selectOption('member');
  if (failStatus) await page.route('**/api/v1/auth/status', (route) => route.abort('failed'));
  const saved = page.waitForResponse((response) => isPath(response.url(), `/api/v1/members/${member.id}`) && response.request().method() === 'PATCH');
  await editor.getByRole('button', { name: 'Save changes' }).click();
  const response = await saved;
  expect(response.status()).toBe(200);
  await attachCapture(info, `self-demotion-${failStatus ? 'status-down' : 'normal'}.json`, await response.json());
  await expect(membersRegion(page)).toHaveCount(0);
  await expect(nav.getByText(project.name, { exact: true })).toHaveCount(0);
  await expect(page.getByRole('region', { name: 'Your account' })).toContainText('No projects granted');
  await attachScreenshot(info, page, `self-demotion-${failStatus ? 'status-down' : 'normal'}.png`);
  await context.close();
  await root.close();
}

export async function sessionOutageRecovery(browser: Browser, info: TestInfo) {
  const context = await signedInContext(browser, owner.email, owner.password);
  const page = await context.newPage();
  await page.route('**/api/v1/auth/sessions', (route) => route.fulfill({ status: 503, json: { error: { code: 'unavailable', message: 'synthetic session outage' } } }));
  await page.goto('/settings');
  const own = page.getByRole('region', { name: 'Signed-in sessions', exact: true });
  await expect(own.getByRole('alert')).toBeVisible();
  await expect(own.getByRole('button', { name: /Retry|Show sessions|Refresh sessions/ })).toBeVisible();
  await page.unroute('**/api/v1/auth/sessions');
  await own.getByRole('button', { name: /Retry|Show sessions|Refresh sessions/ }).click();
  await expect(own.getByRole('button', { name: 'Revoke session:', exact: false }).first()).toBeVisible();
  await attachScreenshot(info, page, 'own-session-outage-recovered.png');
  await context.close();
}

export async function revokedSessionStatusOutage(browser: Browser, info: TestInfo) {
  const root = await signedInContext(browser, owner.email, owner.password);
  const id = runId();
  const person = { name: `Revoke Review ${id}`, email: `revoke.${id}@example.test`, password: syntheticPassword(id), project_ids: [] };
  await createMemberViaApi(root, person);
  await root.close();
  const context = await signedInContext(browser, person.email, person.password, `Review status-outage ${id}`);
  const page = await context.newPage();
  await page.goto('/settings');
  const own = page.getByRole('region', { name: 'Signed-in sessions', exact: true });
  const revoke = own.getByRole('button', { name: /Revoke session: Review status-outage/ });
  await expect(revoke).toBeVisible();
  await page.route('**/api/v1/auth/status', (route) => route.abort('failed'));
  await revoke.click();
  await confirm(page, 'Revoke session');
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible();
  expect(await cookieWorks(context)).toBe(false);
  await attachScreenshot(info, page, 'revoked-session-status-unavailable.png');
  await context.close();
}

export async function lateSessionProbeCannotLogout(browser: Browser, info: TestInfo) {
  const root = await signedInContext(browser, owner.email, owner.password);
  const id = runId();
  const person = { name: `Late Review ${id}`, email: `late.${id}@example.test`, password: syntheticPassword(id), project_ids: [] };
  await createMemberViaApi(root, person);
  await root.close();
  const context = await signedInContext(browser, person.email, person.password, `Review late-probe ${id}`);
  const page = await context.newPage();
  await page.goto('/settings');
  const own = page.getByRole('region', { name: 'Signed-in sessions', exact: true });
  const revoke = own.getByRole('button', { name: /Revoke session: Review late-probe/ });
  await expect(revoke).toBeVisible();
  let release!: () => void;
  let reached!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  const requested = new Promise<void>((resolve) => { reached = resolve; });
  await page.route('**/api/v1/auth/status', async (route) => {
    reached();
    await held;
    await route.fulfill({ json: { mode: 'local', configured: true, setup_required: false, authenticated: false, actor: null, user: null } });
  });
  await revoke.click();
  await confirm(page, 'Revoke session');
  await requested;
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('menuitem', { name: 'Sign out' }).click();
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible();
  await page.getByLabel('Email').fill(owner.email);
  await page.getByLabel('Password', { exact: true }).fill(owner.password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible();
  await recordContextCredentials(context);
  const completed = page.waitForResponse((response) => isPath(response.url(), '/api/v1/auth/status'));
  release();
  await completed;
  await page.waitForTimeout(150);
  await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible();
  expect(await cookieWorks(context)).toBe(true);
  await attachScreenshot(info, page, 'late-revoke-probe-fenced.png');
  await context.close();
}

export async function pendingRecheckFailureVisible(browser: Browser, info: TestInfo) {
  const context = await browser.newContext({ baseURL });
  const page = await context.newPage();
  let reads = 0;
  await page.route('**/api/v1/auth/status', (route) => ++reads > 1 ? route.abort('failed') : route.fulfill({ json: {
    mode: 'cloudflare', configured: true, setup_required: false, authenticated: true, actor: pendingActor, user: pendingActor
  } }));
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Waiting for workspace access' })).toBeVisible();
  await page.getByRole('button', { name: 'Check again' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Check again' })).toBeEnabled();
  await attachScreenshot(info, page, 'pending-recheck-error.png');
  await context.close();
}
