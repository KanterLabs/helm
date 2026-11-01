import { expect, test, type BrowserContext } from '@playwright/test';
import {
  assertNoSecretsInEvidence, attachCapture, attachScreenshot, authMode, baseURL, confirm, cookieWorks, createMemberViaApi,
  createProject, ensureOwner, isPath, loginThroughUi, memberRow, membersRegion, openEditor, owner, runId, signedInContext,
  syntheticPassword, visibleProjectIds
} from './support/membershipFixture';
import { passwordAndSessionJourney } from './support/membershipSessionJourney';
import { admitPendingIdentity, nonLocalAccountControls, pendingAdmissionAndLegacyModes, unavailableAndFencedDirectory } from './support/membershipSyntheticStates';
import { lateSessionProbeCannotLogout, pendingRecheckFailureVisible, revokedSessionStatusOutage, selfDemotionReconciles, sessionOutageRecovery } from './support/membershipReviewJourney';

// TC-113 journeys. "real:" tests use the actual local-auth server, cookies and
// database. "synthetic:" tests rewrite selected responses to reach states a
// local fixture cannot produce; they are not backend proof. Traces/video stay
// off so typed synthetic passwords and cookies never enter evidence.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

test.beforeEach(async ({ request }) => {
  const mode = (await authMode(request)).mode;
  if (process.env.HELM_E2E_MEMBERSHIP_REQUIRED === 'true') expect(mode, 'required membership fixture uses real local auth').toBe('local');
  else test.skip(mode !== 'local', 'requires the dedicated local-auth membership fixture');
  await ensureOwner(request);
});

test.afterEach(async ({}, info) => assertNoSecretsInEvidence(info));

for (const failStatus of [false, true]) test(`review: self-demotion clears privileged state (status unavailable: ${failStatus})`, async ({ browser }, info) => selfDemotionReconciles(browser, info, failStatus));
test('review: own-session list recovers from its initial outage', async ({ browser }, info) => sessionOutageRecovery(browser, info));
test('review: current-session revocation signs out despite status outage', async ({ browser }, info) => revokedSessionStatusOutage(browser, info));
test('review: an old revoke probe cannot sign out a newer login', async ({ browser }, info) => lateSessionProbeCannotLogout(browser, info));
test('review: pending admission exposes recheck errors', async ({ browser }, info) => pendingRecheckFailureVisible(browser, info));

test('real: owner creates, grants, edits, disables and reactivates a member from Settings', async ({ browser }, info) => {
  test.setTimeout(120_000);
  const ownerContext = await signedInContext(browser, owner.email, owner.password);
  const alpha = await createProject(ownerContext, 'Alpha');
  const beta = await createProject(ownerContext, 'Beta');
  const id = runId();
  const member = { name: `Riley Member ${id}`, email: `riley.${id}@example.test`, password: syntheticPassword(`riley-${id}`) };
  const page = await ownerContext.newPage();
  const listing = page.waitForResponse((response) => isPath(response.url(), '/api/v1/members') && response.request().method() === 'GET');
  await page.goto('/settings');
  expect(new URL((await listing).url()).searchParams.get('disabled'), 'list includes disabled identities').toBe('true');
  await expect(memberRow(page, owner.email)).toContainText('Owner');

  await membersRegion(page).getByRole('button', { name: 'Add member' }).click();
  const form = membersRegion(page).getByRole('form', { name: 'Add member' });
  await form.getByLabel('Email', { exact: true }).fill(member.email);
  await form.getByLabel('Full name').fill(member.name);
  await form.getByRole('checkbox', { name: alpha.name }).check();
  await form.getByLabel('Password', { exact: true }).fill(member.password);
  await form.getByLabel('Confirm password').fill(member.password);
  const [created] = await Promise.all([
    page.waitForResponse((response) => isPath(response.url(), '/api/v1/members') && response.request().method() === 'POST'),
    form.getByRole('button', { name: 'Create member' }).click()
  ]);
  expect(created.status()).toBe(201);
  const createdBody = await created.json();
  await expect(memberRow(page, member.email)).toContainText('1 project');
  await expect(memberRow(page, member.email)).toContainText('Active');

  const memberContext = await browser.newContext({ baseURL });
  const memberPage = await memberContext.newPage();
  const adminReads: string[] = [];
  memberPage.on('request', (request) => { if (isPath(request.url(), /^\/api\/v1\/(members|agents|tokens)/)) adminReads.push(new URL(request.url()).pathname); });
  await loginThroughUi(memberPage, member.email, member.password);
  const nav = memberPage.getByRole('navigation', { name: 'Primary navigation' });
  await expect(nav.getByText(alpha.name)).toBeVisible();
  await expect(nav.getByText(beta.name)).toHaveCount(0);
  expect(await visibleProjectIds(memberContext)).toEqual([alpha.id]);
  await memberPage.goto('/settings');
  const account = memberPage.getByRole('region', { name: 'Your account' });
  await expect(account).toContainText('Member');
  await expect(account).toContainText(alpha.name);
  await expect(membersRegion(memberPage)).toHaveCount(0);
  await expect(memberPage.getByRole('heading', { name: 'Agents & tokens' })).toHaveCount(0);
  await expect(memberPage.getByRole('alert')).toHaveCount(0);
  await attachScreenshot(info, memberPage, 'real-member-settings.png');

  const editor = await openEditor(page, member.name, member.email);
  await editor.getByRole('checkbox', { name: alpha.name }).uncheck();
  await editor.getByRole('checkbox', { name: beta.name }).check();
  const [swap] = await Promise.all([
    page.waitForRequest((request) => request.method() === 'PATCH'),
    editor.getByRole('button', { name: 'Save changes' }).click()
  ]);
  expect(swap.postDataJSON(), 'only changed fields are sent').toEqual({ project_ids: [beta.id] });
  expect(swap.headers()['idempotency-key'], 'admin writes carry an idempotency key').toBeTruthy();
  await expect(editor.getByRole('status')).toContainText('Changes saved');
  expect(await visibleProjectIds(memberContext)).toEqual([beta.id]);

  await editor.getByRole('checkbox', { name: beta.name }).uncheck();
  await expect(editor).toContainText('No projects selected');
  const [revoke] = await Promise.all([
    page.waitForRequest((request) => request.method() === 'PATCH'),
    editor.getByRole('button', { name: 'Save changes' }).click()
  ]);
  expect(revoke.postDataJSON()).toEqual({ project_ids: [] });
  await expect(memberRow(page, member.email)).toContainText('No projects');
  expect(await visibleProjectIds(memberContext)).toEqual([]);
  await memberPage.reload();
  await expect(account).toContainText('No projects granted');

  const ownerEditor = await openEditor(page, owner.name, owner.email);
  await ownerEditor.getByLabel('Workspace role').selectOption('member');
  await ownerEditor.getByRole('button', { name: 'Save changes' }).click();
  await expect(ownerEditor.getByRole('alert')).toContainText('at least one active owner or administrator');
  await ownerEditor.getByRole('button', { name: 'Disable member' }).click();
  await confirm(page, 'Disable member');
  await expect(ownerEditor.getByRole('alert')).toContainText('at least one active owner or administrator');
  await expect(memberRow(page, owner.email)).toContainText('Owner');
  await expect(memberRow(page, owner.email)).toContainText('Active');
  await attachScreenshot(info, page, 'real-last-admin-denied.png');

  await editor.getByRole('button', { name: 'Disable member' }).click();
  await confirm(page, 'Disable member');
  await expect(memberRow(page, member.email)).toContainText('Disabled');
  expect(await cookieWorks(memberContext), 'disable ends the member cookie').toBe(false);
  await editor.getByRole('button', { name: 'Reactivate member' }).click();
  await confirm(page, 'Reactivate member');
  await expect(memberRow(page, member.email)).toContainText('Active');
  await editor.getByRole('checkbox', { name: alpha.name }).check();
  await editor.getByRole('button', { name: 'Save changes' }).click();
  await expect(editor.getByRole('status')).toContainText('Changes saved');
  expect(await cookieWorks(memberContext), 'reactivation and restored grants do not revive sessions').toBe(false);
  const fresh = await signedInContext(browser, member.email, member.password);
  expect(await visibleProjectIds(fresh)).toEqual([alpha.id]);
  expect(adminReads, 'members never fetch admin directories').toEqual([]);
  await attachScreenshot(info, page, 'real-owner-members.png');
  await attachCapture(info, 'real-member-responses.json', { created: createdBody, swap: swap.postDataJSON(), revoke: revoke.postDataJSON() });
  await Promise.all([ownerContext.close(), memberContext.close(), fresh.close()]);
});

test('real: password change, reset and session revocation invalidate exactly the intended cookies', async ({ browser }, info) => {
  test.setTimeout(120_000);
  await passwordAndSessionJourney(browser, info);
});

test('real: an uncertain member update retries with the same Idempotency-Key', async ({ browser }, info) => {
  const ownerContext = await signedInContext(browser, owner.email, owner.password);
  const id = runId();
  const member = await createMemberViaApi(ownerContext, { email: `jules.${id}@example.test`, name: `Jules Member ${id}`, password: syntheticPassword(`jules-${id}`), project_ids: [] });
  const page = await ownerContext.newPage();
  const keys: string[] = [];
  await page.route('**/api/v1/members/*', async (route) => {
    if (route.request().method() !== 'PATCH') return route.continue();
    keys.push(route.request().headers()['idempotency-key']);
    if (keys.length === 1) {
      await route.fetch(); // The server commits, then the browser loses the response.
      return route.abort('failed');
    }
    return route.continue();
  });
  await page.goto('/settings');
  const editor = await openEditor(page, member.name, member.email!);
  await editor.getByLabel('Name', { exact: true }).fill(`Jules Renamed ${id}`);
  await editor.getByRole('button', { name: 'Save changes' }).click();
  await expect(editor.getByRole('alert')).toContainText("couldn't confirm");
  await editor.getByRole('button', { name: 'Retry same change' }).click();
  await expect(memberRow(page, member.email!)).toContainText(`Jules Renamed ${id}`);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[1], 'retry reuses the original key').toBe(keys[0]);
  await attachCapture(info, 'real-uncertain-retry.json', { attempts: keys.length, sameKey: keys[0] === keys[1] });
  await ownerContext.close();
});

test('real: settings stay readable at 320, 390 and desktop in light and dark', async ({ browser }, info) => {
  test.setTimeout(180_000);
  const id = runId();
  const ownerContext = await signedInContext(browser, owner.email, owner.password);
  const member = await createMemberViaApi(ownerContext, { email: `avery.${id}@example.test`, name: `Avery Member ${id}`, password: syntheticPassword(`avery-${id}`), project_ids: [] });
  for (const colorScheme of ['light', 'dark'] as const) {
    for (const width of [320, 390, 1280]) {
      for (const who of ['owner', 'member'] as const) {
        const context: BrowserContext = await browser.newContext({ baseURL, colorScheme, viewport: { width, height: 860 } });
        const credentials = who === 'owner' ? { email: owner.email, password: owner.password } : { email: member.email!, password: syntheticPassword(`avery-${id}`) };
        const login = await context.request.post('/api/v1/auth/login', { headers: { Origin: new URL(baseURL).origin }, data: credentials });
        expect(login.status()).toBe(200);
        const page = await context.newPage();
        await page.goto('/settings');
        await expect(page.getByRole('region', { name: 'Your account' })).toBeVisible();
        if (who === 'owner') await openEditor(page, member.name, member.email!);
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
        expect(overflow, `${who} ${width}px ${colorScheme} has no horizontal overflow`).toBeLessThanOrEqual(1);
        await attachScreenshot(info, page, `real-${who}-${width}-${colorScheme}.png`);
        await context.close();
      }
    }
  }
  await ownerContext.close();
});

test('synthetic: pending identity sees admission-required state; disabled-mode fixtures stay admitted', async ({ browser }, info) => {
  await pendingAdmissionAndLegacyModes(browser, info);
});

test('synthetic: Cloudflare and Tailnet accounts get no local password or session controls', async ({ browser }, info) => {
  await nonLocalAccountControls(browser, info);
});

test('synthetic: unavailable directory recovers and late responses stay fenced after sign-out', async ({ browser }, info) => {
  test.setTimeout(90_000);
  await unavailableAndFencedDirectory(browser, info);
});

test('synthetic: owner admits a pending identity with an explicit role and no implicit grants', async ({ browser }, info) => {
  await admitPendingIdentity(browser, info);
});
