import { expect, type Browser, type TestInfo } from '@playwright/test';
import {
  attachScreenshot, baseURL, createMemberViaApi, isPath, loginThroughUi, memberRow, membersRegion, owner, runId,
  recordContextCredentials, signedInContext, syntheticPassword
} from './membershipFixture';

// Synthetic membership states. Each rewrites only the responses named in its
// body to reach states a local fixture cannot produce (pending Cloudflare
// identity, non-local modes, outages, held responses). Not backend proof.
export const pendingActor = { id: 'actor-pending-synthetic', kind: 'human', name: 'Pending Person', email: 'pending@example.test', admin: false, created_at: '2026-10-30T00:00:00Z', updated_at: '2026-10-30T00:00:00Z' };


/** synthetic: pending identity sees admission-required state; disabled-mode fixtures stay admitted */
export async function pendingAdmissionAndLegacyModes(browser: Browser, info: TestInfo): Promise<void> {
  const context = await browser.newContext({ baseURL, viewport: { width: 390, height: 860 } });
  const page = await context.newPage();
  const workspaceReads: string[] = [];
  page.on('request', (request) => { const path = new URL(request.url()).pathname; if (path.startsWith('/api/v1/') && path !== '/api/v1/auth/status') workspaceReads.push(path); });
  let statusReads = 0;
  await page.route('**/api/v1/auth/status', (route) => { statusReads += 1; return route.fulfill({ json: { mode: 'cloudflare', configured: true, setup_required: false, authenticated: true, actor: pendingActor, user: pendingActor } }); });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Waiting for workspace access' })).toBeVisible();
  await expect(page.getByText('pending@example.test')).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Check again' }).click();
  await expect.poll(() => statusReads).toBeGreaterThan(1);
  await expect(page.getByRole('heading', { name: 'Waiting for workspace access' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Check again' })).toBeEnabled();
  expect(workspaceReads, 'a pending identity triggers no workspace reads').toEqual([]);
  await attachScreenshot(info, page, 'synthetic-pending-admission-390.png');
  await context.close();

  const ownerContext = await signedInContext(browser, owner.email, owner.password);
  const devPage = await ownerContext.newPage();
  const legacy = { ...pendingActor, id: 'actor-disabled-mode', name: 'Development user', admin: true };
  await devPage.route('**/api/v1/auth/status', (route) => route.fulfill({ json: { mode: 'disabled', configured: true, setup_required: false, authenticated: true, actor: legacy, user: legacy } }));
  await devPage.goto('/settings');
  await expect(devPage.getByRole('heading', { name: 'Waiting for workspace access' })).toHaveCount(0);
  await expect(membersRegion(devPage)).toBeVisible();
  await ownerContext.close();
}

/** synthetic: Cloudflare and Tailnet accounts get no local password or session controls */
export async function nonLocalAccountControls(browser: Browser, info: TestInfo): Promise<void> {
  const ownerContext = await signedInContext(browser, owner.email, owner.password);
  for (const mode of ['cloudflare', 'tailnet'] as const) {
    const page = await ownerContext.newPage();
    const sessionReads: string[] = [];
    page.on('request', (request) => { if (isPath(request.url(), /sessions/)) sessionReads.push(request.url()); });
    await page.route('**/api/v1/auth/status', async (route) => {
      const real = await (await route.fetch()).json();
      await route.fulfill({ json: { ...real, mode } });
    });
    await page.goto('/settings');
    const account = page.getByRole('region', { name: 'Your account' });
    await expect(account).toContainText(mode === 'cloudflare' ? 'managed by Cloudflare Access' : 'managed by your private Tailnet');
    await expect(page.getByRole('form', { name: 'Change password' })).toHaveCount(0);
    await expect(page.getByRole('region', { name: 'Signed-in sessions' })).toHaveCount(0);
    await membersRegion(page).getByRole('button', { name: 'Add member' }).click();
    await expect(membersRegion(page).getByLabel('Password', { exact: true })).toHaveCount(0);
    expect(sessionReads).toEqual([]);
    await attachScreenshot(info, page, `synthetic-${mode}-account.png`);
    await page.close();
  }
  await ownerContext.close();
}

/** synthetic: unavailable directory recovers and late responses stay fenced after sign-out */
export async function unavailableAndFencedDirectory(browser: Browser, info: TestInfo): Promise<void> {
  const id = runId();
  const ownerContext = await signedInContext(browser, owner.email, owner.password);
  const member = await createMemberViaApi(ownerContext, { email: `quinn.${id}@example.test`, name: `Quinn Member ${id}`, password: syntheticPassword(`quinn-${id}`), project_ids: [] });
  await ownerContext.close();
  const context = await browser.newContext({ baseURL });
  const page = await context.newPage();
  await loginThroughUi(page, owner.email, owner.password);
  const directory = (url: URL) => url.pathname === '/api/v1/members';
  let failures = 1;
  await page.route(directory, (route) => failures-- > 0 ? route.fulfill({ status: 503, json: { error: { code: 'unavailable', message: 'synthetic outage' } } }) : route.continue());
  await page.goto('/settings');
  await expect(membersRegion(page).getByRole('alert')).toContainText('Members could not be loaded');
  await membersRegion(page).getByRole('button', { name: 'Retry' }).click();
  await expect(memberRow(page, member.email!)).toBeVisible();

  await page.unroute(directory);
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route(directory, async (route) => { const response = await route.fetch(); await held; await route.fulfill({ response }); });
  await page.reload();
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('menuitem', { name: 'Sign out' }).click();
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible();
  await page.getByLabel('Email').fill(member.email!);
  await page.getByLabel('Password', { exact: true }).fill(syntheticPassword(`quinn-${id}`));
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  // Stay in the same document so the held owner response can still arrive.
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Your account' })).toContainText(member.name);
  await recordContextCredentials(context);
  release();
  await page.waitForTimeout(500);
  await expect(membersRegion(page)).toHaveCount(0);
  await expect(page.getByText(owner.email)).toHaveCount(0);
  await attachScreenshot(info, page, 'synthetic-fenced-member-view.png');
  await context.close();
}

/** synthetic: owner admits a pending identity with an explicit role and grants */
export async function admitPendingIdentity(browser: Browser, info: TestInfo): Promise<void> {
  const ownerContext = await signedInContext(browser, owner.email, owner.password);
  const page = await ownerContext.newPage();
  let admission: Record<string, unknown> | undefined;
  // Inject one verified-but-unadmitted identity into the real directory page
  // and answer its PATCH; every other request reaches the real server.
  await page.route((url) => url.pathname === '/api/v1/members', async (route) => {
    const real = await (await route.fetch()).json() as { data: unknown[]; next_cursor: string };
    await route.fulfill({ json: { ...real, data: [...real.data, pendingActor] } });
  });
  await page.route(`**/api/v1/members/${pendingActor.id}`, async (route) => {
    admission = route.request().postDataJSON();
    await route.fulfill({ json: { ...pendingActor, workspace_role: 'member', project_ids: [], updated_at: '2026-10-30T01:00:00Z' } });
  });
  await page.goto('/settings');
  const row = memberRow(page, pendingActor.email);
  await expect(row).toContainText('Awaiting admission');
  await row.getByRole('button', { name: `Manage ${pendingActor.name}` }).click();
  const editor = page.getByRole('region', { name: `Edit ${pendingActor.name}` });
  await expect(editor).toContainText('has no access yet');
  await expect(editor.getByRole('button', { name: 'Disable member' })).toHaveCount(0);
  await editor.getByRole('button', { name: 'Admit to workspace' }).click();
  await expect(editor.getByRole('status')).toContainText('was admitted');
  await expect(row).toContainText('Active');
  await expect(row).toContainText('No projects');
  expect(admission, 'admission sends an explicit role and no implicit grants').toEqual({ role: 'member' });
  await attachScreenshot(info, page, 'synthetic-admitted-pending.png');
  await ownerContext.close();
}
