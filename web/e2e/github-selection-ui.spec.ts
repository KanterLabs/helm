import { expect, test, type Page, type Route } from '@playwright/test';
import { expectNoAuditMutations } from './support/auditContainment';
import { API, columns, installGithubMocks, json, project, WEB } from './support/githubFixture';

// Synthetic API overlays exercise the real mounted UI and in-app navigation.
// TC-316 parks the audit-page repository editor and audit start; selection
// stays in admin Settings and audit history keeps stale-response protection.
test.use({ serviceWorkers: 'block' });

const other = { ...project, id: 'project-gh-other', key: 'GHO', slug: 'github-other', name: 'Other GitHub project' };
type Selection = { repository: string; ref: string };

async function installProjects(page: Page): Promise<void> {
  await installGithubMocks(page);
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '');
    if (path === '/projects') return json(route, { data: [project, other], next_cursor: null });
    if (path.startsWith(`/projects/${other.id}/`)) {
      if (path.endsWith('/columns')) return json(route, { data: columns.map((column) => ({ ...column, project_id: other.id })), next_cursor: null });
      if (!path.includes('/github')) return json(route, { data: [], next_cursor: null });
    }
    return route.fallback();
  });
}

async function changeProject(page: Page): Promise<void> {
  await page.evaluate((slug) => {
    history.pushState({}, '', `/p/${slug}/audits`);
    dispatchEvent(new PopStateEvent('popstate'));
  }, other.slug);
  await expect(page).toHaveURL(`/p/${other.slug}/audits`);
  await expect(page.getByRole('heading', { name: 'Board audits', exact: true })).toBeVisible();
}

test('clears old selection while a project loads and ignores its late save', async ({ page }, testInfo) => {
  await installProjects(page);
  let oldSave: Route | undefined;
  let nextSelection: Route | undefined;
  const writes: Array<{ path: string; version: string | undefined; body: unknown }> = [];
  await page.route('**/api/v1/projects/*/github', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const oldProject = path.includes(`/projects/${project.id}/`);
    if (request.method() === 'PUT') {
      writes.push({ path, version: request.headers()['if-match'], body: request.postDataJSON() });
      if (oldProject) { oldSave = route; return; }
      return json(route, { version: 10, repositories: request.postDataJSON().repositories });
    }
    if (!oldProject) { nextSelection = route; return; }
    // The backend sorts pairs by repository/ref; allowlist order differs.
    return json(route, { version: 4, repositories: [{ repository: API, ref: 'next' }, { repository: WEB, ref: 'main' }] });
  });
  await page.goto('/settings');
  const settings = page.locator('[data-github-settings]');
  const panel = settings.locator('[data-github-repositories]');
  await expect(panel.getByRole('button', { name: 'Save repositories' })).toBeDisabled();
  await panel.getByLabel(`Branch for ${WEB}`, { exact: true }).fill('release');
  await panel.getByRole('button', { name: 'Save repositories' }).click();
  await expect.poll(() => Boolean(oldSave)).toBe(true);
  // The wrapping label's name includes the selected option, so target the control.
  await settings.locator('.github-project select').selectOption(other.id);
  await expect.poll(() => Boolean(nextSelection)).toBe(true);
  await expect(panel.getByRole('textbox')).toHaveCount(0);
  await expect(panel.getByRole('button', { name: 'Save repositories' })).toHaveCount(0);
  const response = page.waitForResponse((item) => item.request().method() === 'PUT' && item.url().includes(`/projects/${project.id}/github`));
  await json(oldSave!, { version: 5, repositories: [{ repository: WEB, ref: 'release' }] });
  await (await response).finished();
  await json(nextSelection!, { version: 9, repositories: [{ repository: API, ref: 'other-only' }] });
  await expect(panel.getByLabel(`Branch for ${API}`, { exact: true })).toHaveValue('other-only');
  await expect(panel.getByRole('checkbox', { name: WEB, exact: true })).not.toBeChecked();
  await expect(panel.getByRole('button', { name: 'Save repositories' })).toBeDisabled();
  await expect(panel).not.toContainText('Repositories saved');
  await panel.getByLabel(`Branch for ${API}`, { exact: true }).fill('other-next');
  await panel.getByRole('button', { name: 'Save repositories' }).click();
  await expect(panel).toContainText('Repositories saved');
  expect(writes[1]).toEqual({ path: `/api/v1/projects/${other.id}/github`, version: '"v9"', body: { repositories: [{ repository: API, ref: 'other-next' }] } });
  await testInfo.attach('selection-project-versions', { body: JSON.stringify(writes, null, 2), contentType: 'application/json' });
});

test('keeps every selected branch when saving another repository', async ({ page }, testInfo) => {
  await installGithubMocks(page);
  const writes: Selection[][] = [];
  let selection = { version: 4, repositories: [{ repository: WEB, ref: 'main' }, { repository: WEB, ref: 'release' }] };
  await page.route(`**/api/v1/projects/${project.id}/github`, async (route) => {
    if (route.request().method() === 'PUT') {
      expect(route.request().headers()['if-match']).toBe(`"v${selection.version}"`);
      writes.push(route.request().postDataJSON().repositories);
      selection = { version: selection.version + 1, repositories: writes[writes.length - 1] };
    }
    return json(route, selection);
  });
  await page.goto('/settings');
  const panel = page.locator('[data-github-settings] [data-github-repositories]');
  await expect(panel.getByRole('textbox', { name: `Branch for ${WEB}`, exact: false })).toHaveCount(2);
  await expect(panel.getByRole('button', { name: 'Save repositories' })).toBeDisabled();
  await panel.getByRole('checkbox', { name: API, exact: true }).check();
  await panel.getByLabel(`Branch for ${API}`, { exact: true }).fill('next');
  await panel.getByRole('button', { name: 'Save repositories' }).click();
  await expect(panel).toContainText('Repositories saved');
  expect(writes).toEqual([[{ repository: WEB, ref: 'main' }, { repository: WEB, ref: 'release' }, { repository: API, ref: 'next' }]]);
  await testInfo.attach('saved-repository-pairs', { body: JSON.stringify(writes, null, 2), contentType: 'application/json' });
  await page.screenshot({ path: testInfo.outputPath('multiple-branches-preserved.png'), fullPage: true });
});

test('the audit page has no repository editor or start, and ignores a late previous-project list', async ({ page }, testInfo) => {
  await installProjects(page);
  let oldList: Route | undefined;
  await page.route(new RegExp(`/api/v1/projects/${project.id}/audits(?:\\?|$)`), (route) => { oldList = route; });
  await page.goto(`/p/${project.slug}/audits`);
  await expect.poll(() => Boolean(oldList)).toBe(true);
  await expectNoAuditMutations(page);
  await changeProject(page);
  await expect(page.getByRole('heading', { name: 'No recorded board audits', exact: true })).toBeVisible();
  const oldResponse = page.waitForResponse((response) => response.url().includes(`/projects/${project.id}/audits`));
  await json(oldList!, { data: [{ id: 'audit-late-old-project', project_id: project.id, scope: 'board', status: 'complete', finding_count: 2, created_at: '2026-10-26T10:00:00Z' }], next_cursor: null });
  await (await oldResponse).finished();
  // A browser frame drains the response callback before checking its side effect.
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  await expect(page).toHaveURL(`/p/${other.slug}/audits`);
  await expect(page.getByRole('button', { name: /audit-late-old-project/ })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'No recorded board audits', exact: true })).toBeVisible();
  await expectNoAuditMutations(page);
  await page.screenshot({ path: testInfo.outputPath('late-previous-project-list-ignored.png'), fullPage: true });
});
