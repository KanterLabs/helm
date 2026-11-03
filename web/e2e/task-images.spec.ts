import { expect, test } from '@playwright/test';

// Written before implementation. Failure scenarios: uploads unavailable or
// public; forged file types; wrong-project/task links; duplicate publication
// after response loss; images disappearing from older progress; inaccessible
// viewer/comparison; deleted originals still readable; missing backup objects.
// Fixtures contain no private user content or credentials.
test('publishes screenshot evidence and keeps original downloads private', async ({ page, request }, testInfo) => {
  const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
  const headers = { Origin: origin };
  const suffix = Date.now().toString(36).toUpperCase();
  const projectResponse = await request.post('/api/v1/projects', { headers, data: { key: `IMG${suffix}`.slice(0, 16), name: `Screenshot evidence ${suffix}` } });
  expect(projectResponse.ok()).toBeTruthy();
  const project = await projectResponse.json();
  const columns = await (await request.get(`/api/v1/projects/${project.id}/columns`)).json();
  const created = await request.post(`/api/v1/projects/${project.id}/tasks`, { headers, data: { title: 'Review inventory screen', column_id: columns.data[0].id } });
  expect(created.ok()).toBeTruthy();
  const task = await created.json();
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNwaDgAAAKEAYEml6crAAAAAElFTkSuQmCC', 'base64');
  const ids: string[] = [];
  for (const caption of ['Before inventory layout', 'After inventory layout']) {
    const response = await request.post(`/api/v1/tasks/${task.id}/attachments`, {
      headers: { ...headers, 'Idempotency-Key': `image-${suffix}-${ids.length}` },
      multipart: { file: { name: 'inventory.png', mimeType: 'image/png', buffer: png }, caption }
    });
    expect(response.status()).toBe(201);
    const image = await response.json();
    ids.push(image.id);
    expect(image.width).toBe(1);
    expect(image.status).toBe('staged');
  }
  const comment = await request.post(`/api/v1/tasks/${task.id}/comments`, {
    headers: { ...headers, 'Idempotency-Key': `image-comment-${suffix}` },
    data: { body: 'Inventory layout implemented', attachment_ids: [...ids].reverse() }
  });
  expect(comment.status()).toBe(201);
  expect((await comment.json()).attachments).toHaveLength(2);
  const activity = await (await request.get(`/api/v1/tasks/${task.id}/timeline`)).json();
  const published = activity.data.find((item: { comment?: { body: string } }) => item.comment?.body === 'Inventory layout implemented');
  expect(published.comment.attachments.map((image: { id: string }) => image.id)).toEqual([...ids].reverse());
  await page.goto(`/p/${project.slug}/tasks/${task.key}?view=activity`);
  await page.getByRole('tab', { name: 'Activity', exact: true }).click();
  await expect(page.getByText('Inventory layout implemented', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /Before inventory layout/ }).first()).toBeVisible();
  const thumbnail = page.getByRole('button', { name: /Before inventory layout/ }).first().getByRole('img');
  await expect.poll(() => thumbnail.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0)).toBe(true);
  await page.getByRole('button', { name: /Before inventory layout/ }).first().click();
  await expect(page.getByRole('dialog', { name: /image|screenshot/i }).last()).toBeVisible();
  const viewer = page.getByRole('dialog', { name: 'Screenshot viewer', exact: true });
  await expect.poll(() => viewer.getByRole('img').first().evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0)).toBe(true);
  await viewer.getByRole('checkbox', { name: 'Compare', exact: true }).check();
  await expect(viewer.getByRole('img')).toHaveCount(2);
  for (const image of await viewer.getByRole('img').all()) await expect.poll(() => image.evaluate((element: HTMLImageElement) => element.complete && element.naturalWidth > 0)).toBe(true);
  await testInfo.attach('task-image-comparison.png', { contentType: 'image/png', body: await page.screenshot() });
  await page.keyboard.press('Escape');
  await testInfo.attach('task-image-evidence.png', { contentType: 'image/png', body: await page.screenshot() });
  const original = await request.get(`/api/v1/tasks/${task.id}/attachments/${ids[0]}/content`);
  expect(original.status()).toBe(200);
  expect(await original.body()).toEqual(png);
  expect(original.headers()['cache-control']).toContain('no-store');
  const invalid = await request.post(`/api/v1/tasks/${task.id}/attachments`, {
    headers: { ...headers, 'Idempotency-Key': `invalid-image-${suffix}` }, multipart: { file: { name: 'fake.png', mimeType: 'image/png', buffer: Buffer.from('<svg/>') } }
  });
  expect(invalid.status()).toBeGreaterThanOrEqual(400);
  expect(invalid.status()).toBeLessThan(500);
});
