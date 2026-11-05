import { expect, test } from '@playwright/test';
import { readFile } from 'node:fs/promises';

// Written before implementation. Failure scenarios: export omits images;
// review mutates data; apply targets a newly selected project; failed apply
// loses the file/review; retry duplicates a committed copy; stale responses
// cross project/session boundaries; a reviewed invalid archive can be applied.
test('exports project images and reviews a copy before importing into another project', async ({ page, request }, info) => {
  const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
  const headers = { Origin: origin };
  const suffix = Date.now().toString(36).toUpperCase();
  async function createProject(kind: string) {
    const response = await request.post('/api/v1/projects', { headers, data: { key: `${kind}${suffix}`.slice(0, 16), name: `${kind} image bundle ${suffix}` } });
    expect(response.ok()).toBeTruthy();
    return response.json();
  }
  const source = await createProject('SRC');
  const target = await createProject('DST');
  const columns = await (await request.get(`/api/v1/projects/${source.id}/columns`)).json();
  const task = await (await request.post(`/api/v1/projects/${source.id}/tasks`, { headers, data: { title: 'Portable screenshot evidence', column_id: columns.data[0].id } })).json();
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNwaDgAAAKEAYEml6crAAAAAElFTkSuQmCC', 'base64');
  const imageResponse = await request.post(`/api/v1/tasks/${task.id}/attachments`, { headers: { ...headers, 'Idempotency-Key': `bundle-image-${suffix}` }, multipart: { file: { name: 'bundle.png', mimeType: 'image/png', buffer: png }, caption: 'Portable screenshot' } });
  expect(imageResponse.status()).toBe(201);
  const image = await imageResponse.json();
  const published = await request.post(`/api/v1/tasks/${task.id}/comments`, { headers: { ...headers, 'Idempotency-Key': `bundle-comment-${suffix}` }, data: { body: 'Export this evidence', attachment_ids: [image.id] } });
  expect(published.status()).toBe(201);
  await page.goto('/settings');
  const projectPicker = page.locator('#admin-project-select');
  await projectPicker.selectOption(source.id);
  const panel = page.getByRole('region', { name: 'Project files', exact: true });
  const downloaded = page.waitForEvent('download');
  await panel.getByRole('button', { name: 'Export project with images', exact: true }).click();
  const download = await downloaded;
  expect(download.suggestedFilename()).toMatch(/\.zip$/);
  const zip = await readFile((await download.path())!);
  expect(zip.subarray(0, 2).toString()).toBe('PK');
  await projectPicker.selectOption(target.id);
  await panel.getByLabel('Choose project ZIP').setInputFiles({ name: 'project-with-images.zip', mimeType: 'application/zip', buffer: zip });
  await panel.getByRole('button', { name: 'Review import', exact: true }).click();
  await expect(panel.getByRole('heading', { name: 'Review project copy', exact: true })).toBeVisible();
  await expect(panel.getByText(target.name, { exact: true })).toBeVisible();
  const before = await (await request.get(`/api/v1/projects/${target.id}/tasks`)).json();
  expect(before.data).toHaveLength(0);
  await page.route('**/api/v1/import/bundle?**', async (route) => {
    if (new URL(route.request().url()).searchParams.get('dry_run') === 'false') await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { message: 'Try importing this copy again' } }) });
    else await route.continue();
  });
  await panel.getByRole('button', { name: 'Import copy into selected project', exact: true }).click();
  const confirmation = page.getByRole('alertdialog', { name: `Import a copy into ${target.name}?`, exact: true });
  await confirmation.getByRole('button', { name: 'Import copy', exact: true }).click();
  await expect(panel.getByText('Try importing this copy again', { exact: true })).toBeVisible();
  await expect(panel.getByText('project-with-images.zip', { exact: true }).first()).toBeVisible();
  await page.unroute('**/api/v1/import/bundle?**');
  await panel.getByRole('button', { name: 'Import copy into selected project', exact: true }).click();
  await confirmation.getByRole('button', { name: 'Import copy', exact: true }).click();
  await expect(panel.getByText('Project copy imported.', { exact: true })).toBeVisible();
  const after = await (await request.get(`/api/v1/projects/${target.id}/tasks`)).json();
  expect(after.data).toHaveLength(1);
  const copiedImages = await (await request.get(`/api/v1/tasks/${after.data[0].id}/attachments`)).json();
  expect(copiedImages.data).toHaveLength(1);
  const original = await request.get(`/api/v1/tasks/${after.data[0].id}/attachments/${copiedImages.data[0].id}/content`);
  expect(await original.body()).toEqual(png);
  await info.attach('project-image-copy-review.png', { contentType: 'image/png', body: await page.screenshot() });
});
