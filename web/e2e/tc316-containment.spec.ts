import { readFile } from 'node:fs/promises';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

// TC-316 containment against the real fixture server with synthetic projects.
// Failure inventory: docs/TC316_UI_CONTAINMENT_FAILURES.md (T1–T4, R1–R5).
// - transfer returned after TC-317's v4 acceptance: export is not v4, preview
//   writes, or an import lands without the explicit confirmation;
// - Roadmap says Completed/Shipped, synthesizes counts, describes workspace
//   scope inside a project, lists other projects there, or shows zeros as
//   facts when the summary failed or the project has no tasks.
const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
const headers = () => ({ Origin: origin, 'Idempotency-Key': crypto.randomUUID() });
type Project = { id: string; slug: string; name: string };

async function createProject(request: APIRequestContext, label: string, tasks: Array<'open' | 'complete'>): Promise<Project> {
  const suffix = crypto.randomUUID().slice(0, 6).toUpperCase();
  const response = await request.post('/api/v1/projects', { headers: headers(), data: {
    key: `C${label}${suffix}`, name: `Containment ${label} ${suffix}`, description: 'Synthetic TC-316 containment project.'
  } });
  expect(response.ok(), await response.text()).toBeTruthy();
  const project = await response.json() as Project;
  const columns = (await (await request.get(`/api/v1/projects/${project.id}/columns`)).json()).data as { id: string; semantic_state: string }[];
  const open = columns.find((column) => column.semantic_state === 'backlog') || columns[0];
  const done = columns.find((column) => column.semantic_state === 'completed');
  expect(done, 'default board has a completed column').toBeTruthy();
  for (const [index, state] of tasks.entries()) {
    const created = await request.post(`/api/v1/projects/${project.id}/tasks`, { headers: headers(), data: {
      title: `Synthetic ${state} task ${index + 1}`, column_id: state === 'complete' ? done!.id : open.id
    } });
    expect(created.ok(), await created.text()).toBeTruthy();
  }
  return project;
}

async function taskTitles(request: APIRequestContext, project: Project): Promise<string[]> {
  const response = await request.get(`/api/v1/projects/${project.id}/tasks`);
  expect(response.ok()).toBeTruthy();
  return ((await response.json()).data as { title: string }[]).map((task) => task.title).sort();
}

const card = (page: Page, label: string) => page.locator('.metric-card').filter({ hasText: label });

test('restored transfer exports v4 and previews read-only before an explicit import', async ({ page, request }, info) => {
  const source = await createProject(request, 'S', ['open', 'complete']);
  const destination = await createProject(request, 'D', []);
  const imports: Array<{ dryRun: unknown }> = [];
  page.on('request', (item) => {
    if (item.method() === 'POST' && new URL(item.url()).pathname === '/api/v1/import') imports.push({ dryRun: item.postDataJSON()?.dry_run });
  });
  await page.goto(`/p/${source.slug}`);
  await expect(page.getByRole('heading', { name: source.name, exact: true })).toBeVisible();
  await page.locator('[data-board-actions-trigger]').click();
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Export project', exact: true }).click();
  const archivePath = info.outputPath('synthetic-portable-archive.json');
  await (await download).saveAs(archivePath);
  expect(JSON.parse(await readFile(archivePath, 'utf8')).version).toBe(4);

  await page.goto(`/p/${destination.slug}`);
  await expect(page.getByRole('heading', { name: destination.name, exact: true })).toBeVisible();
  await page.locator('[data-board-actions-trigger]').click();
  const chooser = page.waitForEvent('filechooser');
  await page.getByRole('button', { name: 'Import archive', exact: true }).click();
  await (await chooser).setFiles(archivePath);
  const preview = page.getByRole('dialog', { name: 'Review import' });
  await expect(preview).toContainText('tasks to create');
  expect(imports).toEqual([{ dryRun: true }]);
  expect(await taskTitles(request, destination)).toEqual([]);
  await info.attach('transfer-preview.png', { body: await page.screenshot(), contentType: 'image/png' });
  await preview.getByRole('button', { name: 'Import archive', exact: true }).click();
  await expect(preview).toBeHidden();
  expect(imports.at(-1)).toEqual({ dryRun: undefined });
  expect(await taskTitles(request, destination)).toEqual(await taskTitles(request, source));
});

test('project Roadmap reports tasks marked complete for that project only', async ({ page, request }, info) => {
  const scoped = await createProject(request, 'A', ['open', 'complete']);
  const other = await createProject(request, 'B', ['open']);
  await page.goto(`/p/${scoped.slug}/roadmap`);
  await expect(page.getByRole('heading', { name: `${scoped.name} progress` })).toBeVisible();
  const hero = page.locator('.roadmap-hero');
  await expect(hero).toContainText('2 total tasks');
  await expect(hero).toContainText('1 marked complete');
  await expect(hero).toContainText("this project's task states");
  await expect(card(page, 'Tasks marked complete')).toContainText('1');
  await expect(card(page, 'Completion')).toContainText('In this project');
  const content = page.locator('.content');
  for (const claim of [/shipped/i, 'Workspace pulse', 'Across all projects', "each project's"]) await expect(content.getByText(claim)).toHaveCount(0);
  const progress = page.locator('.project-progress-panel');
  await expect(progress.locator('.project-progress-row')).toHaveCount(1);
  await expect(progress).toContainText(scoped.name);
  await expect(progress).not.toContainText(other.name);
  await info.attach('project-roadmap.png', { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });

  await page.goto('/roadmap');
  await expect(page.getByRole('heading', { name: 'Roadmap overview' })).toBeVisible();
  await expect(card(page, 'Tasks marked complete')).toBeVisible();
  await expect(card(page, 'Completion')).toContainText('Across your projects');
  await expect(page.locator('.content').getByText(/shipped/i)).toHaveCount(0);
  await expect(page.locator('.project-progress-panel')).toContainText(other.name);
});

test('Roadmap is honest when a summary fails or a project has no tasks', async ({ page, request }, info) => {
  const empty = await createProject(request, 'E', []);
  await page.route('**/api/v1/projects/*/roadmap', (route) => route.fulfill({
    status: 503, contentType: 'application/json',
    body: JSON.stringify({ error: { code: 'unavailable', message: 'Synthetic summary outage.' } })
  }));
  await page.goto(`/p/${empty.slug}/roadmap`);
  const unavailable = page.locator('[data-roadmap-unavailable]');
  await expect(unavailable).toContainText('Progress figures are unavailable');
  await expect(page.locator('.metric-card')).toHaveCount(0);
  await expect(page.getByText(/\b0 total tasks\b/)).toHaveCount(0);
  await info.attach('roadmap-unavailable.png', { body: await page.screenshot(), contentType: 'image/png' });

  await page.unroute('**/api/v1/projects/*/roadmap');
  await unavailable.getByRole('button', { name: 'Retry' }).click();
  await expect(unavailable).toHaveCount(0);
  await expect(page.locator('.roadmap-hero')).toContainText('No tasks yet');
  await expect(card(page, 'Completion')).toContainText('No tasks yet');
  await expect(page.locator('.roadmap-hero')).not.toContainText('0%');
});
