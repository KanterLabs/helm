import { expect, test } from '@playwright/test';
import {
  adminContext, ADMIN_TWO, api, artifact, AUTHORIZATIONS_URL, BETA, connect, DOCS, INSTALL_URL, LOGIN, panelOf, PR_TITLE,
  expectHealthy, readCode, REFRESH_DUE, refused, seed, settingsOf, shoot, signIn, ADMIN_ONE, testConnection, upstream, watchResponses
} from './support/githubDeviceFlowFixture';

// Device-flow GitHub sign-in in a real browser against the real binary
// (test/githubbeta, device-flow mode). Nothing is routed or mocked in the
// browser: only GitHub itself is synthetic. Covers test/githubbeta/FAILURES.md
// "Device-flow sign-in against the real binary".
test.use({ trace: 'on' });
test.describe.configure({ mode: 'serial' });

test.describe('GitHub device-flow sign-in, real backend', () => {
  test.skip(process.env.HELM_GITHUB_BETA_E2E !== '1', 'Dedicated synthetic upstream harness required');
  test.setTimeout(180_000);

  let leaks: string[] = [];
  test.beforeEach(async ({ page, context }) => {
    await signIn(context, ADMIN_ONE);
    // Each test starts disconnected, with nothing installed and a true clock.
    await api(page.request, 'POST', '/integrations/github/disconnect');
    await upstream(page.request, { reset_clock: true, installed: [] });
    await page.setViewportSize({ width: 1280, height: 900 });
    leaks = watchResponses(page);
  });

  test.afterEach(async ({ page }) => {
    const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
    expect(storage.includes('SENTINEL'), 'browser storage holds no synthetic secret').toBeFalsy();
    expect(leaks, 'no API answer carried a synthetic secret').toEqual([]);
  });

  test('connects, installs, verifies, refreshes, loses a repository and disconnects', async ({ page }, info) => {
    const { project, task } = await seed(page.request);
    await page.goto('/settings');
    const panel = panelOf(page);
    let navigations = 0;
    page.on('framenavigated', (frame) => { if (frame === page.mainFrame()) navigations += 1; });

    // Connect: one press, a code, and the panel follows GitHub by itself.
    await panel.getByRole('button', { name: 'Connect GitHub' }).click();
    const code = await readCode(page);
    await expect(panel.getByRole('button', { name: 'Copy code' })).toBeVisible();
    await shoot(page, info, 'pending-code');
    const before = navigations;
    await upstream(page.request, { action: 'approve', user_code: code });
    await expect(panel.getByRole('heading', { name: 'One more step: choose repositories' })).toBeVisible({ timeout: 25_000 });
    expect(navigations, 'the panel updated without a reload').toBe(before);
    await expect(panel.locator('[data-github-user-code]')).toHaveCount(0);
    await expect(panel.getByRole('link', { name: /Install Helm on your repositories/ })).toHaveAttribute('href', INSTALL_URL);
    expect((await upstream(page.request)).credential).toEqual({ exists: true, regular: true, file_mode: '0600', dir_mode: '0700' });
    await shoot(page, info, 'no-repositories');

    // Install on GitHub, then "I've installed it" finds both repositories.
    await upstream(page.request, { installed: [BETA, DOCS] });
    await testConnection(page, 'I’ve installed it');
    await expect(panel.getByRole('heading', { name: `Connected as @${LOGIN}` })).toBeVisible();
    await expect(panel).toContainText('2 repositories available');

    // A restart loads the connection back from the credential file.
    expect((await page.request.post('/__githubbeta/restart')).ok()).toBeTruthy();
    await page.reload();
    await expect(panel).toContainText('2 repositories available');
    await expectHealthy(page);

    // Select the repository for the project and save.
    const settings = settingsOf(page);
    await settings.getByLabel('Project').selectOption({ label: project.name });
    await settings.getByRole('checkbox', { name: BETA }).check();
    await settings.getByLabel(`Branch for ${BETA}`, { exact: true }).fill('main');
    await settings.getByRole('button', { name: 'Save repositories' }).click();
    await expect(settings.locator('.gh-repos-status')).toContainText('Repositories saved.');
    await shoot(page, info, 'connected');

    // Verify a pull request on the task.
    const taskPage = `/p/${project.slug}/tasks/${task.key}`;
    await page.goto(taskPage);
    await page.getByLabel('Pull request URL').fill(`https://github.com/${BETA}/pull/42`);
    await page.getByRole('button', { name: 'Link pull request' }).click();
    await expect(page.getByText(PR_TITLE, { exact: false })).toBeVisible();
    expect((await artifact(page.request, task.id)).verification).toBe('verified');

    // Refresh because the token is nearly expired: the PR stays verified.
    // Read without any GitHub call, so nothing could re-verify it meanwhile.
    const start = await upstream(page.request);
    await upstream(page.request, { advance_seconds: REFRESH_DUE });
    await page.goto('/settings');
    await testConnection(page);
    await expect(panel.locator('[data-github-check-note]')).toContainText('Test passed.');
    const refreshed = await upstream(page.request);
    expect(refreshed.refreshes).toBe(start.refreshes + 1);
    expect(refreshed.token_seq).toBe(start.token_seq + 1);
    let pr = await artifact(page.request, task.id);
    expect(pr.verification).toBe('verified');
    expect(pr.snapshot?.title).toBe(PR_TITLE);

    // GitHub refuses the current token (401): Helm refreshes and recovers.
    await upstream(page.request, { reject_access: true, advance_seconds: 120 });
    await testConnection(page);
    await expect(panel.locator('[data-github-check-note]')).toContainText('Test passed.');
    expect((await upstream(page.request)).refreshes).toBe(refreshed.refreshes + 1);
    pr = await artifact(page.request, task.id);
    expect(pr.verification).toBe('verified');
    await page.goto(taskPage);
    await expect(page.getByText(PR_TITLE, { exact: false })).toBeVisible();
    await expect(page.locator('.pr-state').first()).toHaveText('Merged');
    await shoot(page, info, 'pr-verified-after-refresh', page.locator('.pr-state').first());

    // Remove helm-beta on GitHub, test the connection: Helm loses it.
    await upstream(page.request, { installed: [DOCS] });
    await page.goto('/settings');
    await testConnection(page);
    await expect(panel).toContainText('1 repository available');
    await settings.getByLabel('Project').selectOption({ label: project.name });
    await expect(settings.getByRole('checkbox', { name: BETA })).toHaveCount(0);
    await expect(settings.getByRole('checkbox', { name: DOCS })).toBeVisible();
    await expect(settings).toContainText(`No longer allowed by the server and removed on save: ${BETA}`);
    const integration = await api(page.request, 'GET', '/integrations/github');
    expect(integration.allowed_repositories).toEqual([DOCS]);
    pr = await artifact(page.request, task.id);
    expect(pr.verification).toBe('unavailable');
    expect(pr.snapshot ?? null).toBeNull();
    const selection = await api(page.request, 'GET', `/projects/${project.id}/github`);
    expect(await refused(page.request, 'PUT', `/projects/${project.id}/github`, { repositories: [{ repository: BETA, ref: 'main' }] }, selection.version)).toBe(400);
    expect(await refused(page.request, 'POST', `/projects/${project.id}/github-audits`, { repositories: [{ repository: BETA, ref: 'main' }], states: ['backlog'] })).toBeGreaterThanOrEqual(400);
    await shoot(page, info, 'repository-lost');
    await page.goto(taskPage);
    await expect(page.getByText('Helm can’t read this pull request now', { exact: false })).toBeVisible();
    await expect(page.getByText(PR_TITLE, { exact: false })).toHaveCount(0);
    await shoot(page, info, 'pr-unavailable', page.locator('.pr-state').first());

    // Disconnect, confirmed: the file is gone and Helm stops reading GitHub.
    await page.goto('/settings');
    await panel.getByRole('button', { name: 'Disconnect' }).click();
    const confirm = panel.locator('[data-github-disconnect-confirm]');
    await expect(confirm.getByRole('heading', { name: 'Disconnect GitHub?' })).toBeVisible();
    await expect(confirm.getByRole('link', { name: /authorized GitHub Apps/ })).toHaveAttribute('href', AUTHORIZATIONS_URL);
    await shoot(page, info, 'disconnect-confirm');
    await confirm.getByRole('button', { name: 'Disconnect GitHub' }).click();
    await expect(panel.getByRole('button', { name: 'Connect GitHub' })).toBeVisible();
    await shoot(page, info, 'disconnected');
    const after = await upstream(page.request);
    expect(after.credential.exists).toBe(false);
    const doc = await api(page.request, 'GET', '/integrations/github');
    expect(doc.connection.state).toBe('disconnected');
    expect(doc.configured).toBe(false);
    expect(doc.allowed_repositories).toEqual([]);
    expect(await refused(page.request, 'POST', `/tasks/${task.id}/artifacts/${pr.id}/refresh`)).toBeGreaterThanOrEqual(400);
    expect((await upstream(page.request)).upstream_requests, 'no GitHub request after disconnect').toBe(after.upstream_requests);
  });

  test('a denied or expired attempt removes its code and offers Try again', async ({ page }, info) => {
    await page.goto('/settings');
    const panel = panelOf(page);
    await panel.getByRole('button', { name: 'Connect GitHub' }).click();
    const first = await readCode(page);
    await upstream(page.request, { action: 'deny' });
    await expect(panel.getByRole('heading', { name: 'Sign-in was declined' })).toBeVisible({ timeout: 25_000 });
    await expect(page.locator('body')).not.toContainText(first);
    await expect(panel.locator('[data-github-user-code]')).toHaveCount(0);
    await shoot(page, info, 'denied');

    await panel.getByRole('button', { name: 'Try again' }).click();
    const second = await readCode(page);
    expect(second).not.toBe(first);
    await upstream(page.request, { action: 'expire' });
    await expect(panel.getByRole('heading', { name: 'The code expired' })).toBeVisible({ timeout: 25_000 });
    await expect(page.locator('body')).not.toContainText(second);
    await shoot(page, info, 'expired');

    await panel.getByRole('button', { name: 'Try again' }).click();
    const third = await readCode(page);
    expect([first, second]).not.toContain(third);
    await panel.getByRole('button', { name: 'Cancel' }).click();
    await expect(panel.getByRole('button', { name: 'Connect GitHub' })).toBeVisible();
    await expect(page.locator('body')).not.toContainText(third);
  });

  test('a second administrator never sees the code', async ({ page, browser }, info) => {
    await page.goto('/settings');
    const panel = panelOf(page);
    await panel.getByRole('button', { name: 'Connect GitHub' }).click();
    const code = await readCode(page);

    const other = await adminContext(browser, ADMIN_TWO);
    const second = await other.newPage();
    const otherLeaks = watchResponses(second, () => [code]);
    await second.goto('/settings');
    const theirs = panelOf(second);
    await expect(theirs.getByRole('heading', { name: 'Another administrator is connecting GitHub' })).toBeVisible();
    await expect(second.locator('body')).not.toContainText(code);
    await expect(theirs.locator('[data-github-user-code]')).toHaveCount(0);
    const seen = await api(second.request, 'GET', '/integrations/github');
    expect(seen.connection.attempt.state).toBe('pending');
    expect(seen.connection.attempt.user_code).toBeUndefined();
    expect(JSON.stringify(seen)).not.toContain(code);
    await shoot(second, info, 'second-admin');
    // Looking does not disturb the first administrator's attempt.
    await expect(panel.locator('[data-github-user-code]')).toHaveText(code);

    // Taking over replaces the code: the first administrator's goes away.
    await theirs.getByRole('button', { name: 'Connect it myself instead' }).click();
    const replacement = await readCode(second);
    expect(replacement).not.toBe(code);
    await expect(panel.getByRole('heading', { name: 'Another administrator is connecting GitHub' })).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('body')).not.toContainText(code);
    await expect(page.locator('body')).not.toContainText(replacement);
    await theirs.getByRole('button', { name: 'Cancel' }).click();
    await expect(theirs.getByRole('button', { name: 'Connect GitHub' })).toBeVisible();
    expect(otherLeaks, 'the second administrator received no secret and not the first code').toEqual([]);
    await other.close();
  });

  test('a refused refresh ends in Reconnect GitHub, and reconnecting works', async ({ page }, info) => {
    await page.goto('/settings');
    const panel = panelOf(page);
    await connect(page, [BETA]);
    await expectHealthy(page);
    await upstream(page.request, { fail_next_refresh: true, advance_seconds: REFRESH_DUE });
    await testConnection(page);
    await expect(panel.getByRole('heading', { name: 'GitHub sign-in needs renewing' })).toBeVisible();
    await expect(panel.getByRole('button', { name: 'Reconnect GitHub' })).toBeVisible();
    await expect(panel.getByRole('alert')).toHaveText(/GitHub sign-in has ended. Press Reconnect GitHub to connect again./);
    await expect(panel).toContainText(`Helm’s GitHub sign-in for @${LOGIN} expired`);
    const doc = await api(page.request, 'GET', '/integrations/github');
    expect(doc.connection.state).toBe('reconnect_required');
    expect(doc.allowed_repositories).toEqual([]);
    const ended = await upstream(page.request);
    expect(ended.credential).toEqual({ exists: true, regular: true, file_mode: '0600', dir_mode: '0700' });
    await shoot(page, info, 'reconnect-required');

    await upstream(page.request, { reset_clock: true });
    await connect(page, [BETA]);
    await expect(panel).toContainText('1 repository available');
    expect((await api(page.request, 'GET', '/integrations/github')).connection.state).toBe('connected');
    expect((await upstream(page.request)).token_seq).toBe(ended.token_seq + 1);
    await expect(page.locator('body')).not.toContainText(/WPFV-\d{4}/);
  });
});
