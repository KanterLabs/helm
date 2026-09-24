import { expect, test } from '@playwright/test';

type Project = { id: string; key: string; slug: string };
type LunaRun = {
  id: string;
  project_key?: string;
  feature: string;
  outcome: string;
  model: string;
  effort: string;
  thread_id?: string;
  turn_id?: string;
  steps?: { sequence: number; kind: string; at: string }[];
};

test('persists a real Luna turn and exposes private diagnostics in Settings', async ({ page, request }, testInfo) => {
  const auth = await request.get('/api/v1/auth/status');
  expect(auth.ok()).toBeTruthy();
  expect((await auth.json()).mode).toBe('disabled');

  const runId = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const projectKey = `LUNA${runId}`.slice(0, 16);
  const roughIdea = `private-e2e-prompt-${runId}`;
  const origin = new URL(testInfo.project.use.baseURL as string).origin;
  const created = await request.post('/api/v1/projects', {
    data: { key: projectKey, name: `Luna E2E ${runId}` },
    headers: {
      Origin: origin,
      'Content-Type': 'application/json',
      'Idempotency-Key': `luna-history-${runId}`
    }
  });
  expect(created.ok()).toBeTruthy();
  const project = await created.json() as Project;

  await page.goto(`/p/${project.slug}`);
  await page.getByRole('button', { name: 'New task', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Create a task' });
  await dialog.getByLabel('Rough idea').fill(roughIdea);
  await dialog.getByRole('button', { name: /Assist with Luna/ }).click();
  await expect(dialog.getByRole('heading', { name: 'Review before applying' })).toBeVisible();
  await expect(dialog.locator('[data-luna-field="title"]')).toContainText('Verify persisted Luna history');
  await dialog.getByRole('button', { name: 'Apply all' }).click();
  await expect(dialog.getByLabel('Task title')).toHaveValue('Verify persisted Luna history');
  await page.keyboard.press('Escape');

  await page.goto('/settings');
  await expect(page.getByRole('heading', { name: 'Your Codex subscription' })).toBeVisible();
  const history = page.locator('.luna-history');
  await expect(history.getByRole('heading', { name: 'Recent Luna work' })).toBeVisible();
  const item = history.locator('.luna-run').filter({ hasText: projectKey });
  await expect(item).toContainText('Task draft');
  await expect(item).toContainText('Succeeded');
  await item.getByRole('button').click();
  await expect(item).toContainText('gpt-5.6-luna');
  await expect(item).toContainText('medium');
  await expect(item).toContainText('thread-e2e');
  await expect(item).toContainText('turn-e2e');
  await expect(item.getByText('Input sent to Luna')).toBeVisible();
  await expect(item.locator('.luna-exchange-part').first()).toContainText(roughIdea);
  await expect(item.locator('.luna-exchange-part').last()).toContainText('Verify persisted Luna history');
  const timeline = item.getByRole('list', { name: 'Luna execution steps' });
  await expect(timeline).toBeVisible();
  await expect(timeline).toContainText('Thread started');
  await expect(timeline).toContainText('Turn started');
  await expect(timeline).toContainText('Luna generated a response');
  await expect(timeline).toContainText('Checking result');
  await expect(timeline).toContainText('Outcome recorded');

  const response = await request.get('/api/v1/codex/runs?limit=50');
  expect(response.ok()).toBeTruthy();
  const responseText = await response.text();
  expect(responseText).not.toContain('actor_id');
  expect(responseText).not.toContain(roughIdea);
  expect(responseText).not.toContain('Verify persisted Luna history');
  const payload = JSON.parse(responseText) as { data: LunaRun[] };
  const persisted = payload.data.find((run) => run.project_key === projectKey);
  expect(persisted).toMatchObject({
    feature: 'task_draft',
    outcome: 'succeeded',
    model: 'gpt-5.6-luna',
    effort: 'medium',
    thread_id: 'thread-e2e',
    turn_id: 'turn-e2e'
  });
  expect(persisted?.steps?.map((step) => step.kind)).toEqual([
    'thread_started', 'turn_started', 'response_generated', 'validation', 'outcome'
  ]);
  const detailResponse = await request.get(`/api/v1/codex/runs/${persisted?.id}`);
  expect(detailResponse.ok()).toBeTruthy();
  expect(detailResponse.headers()['cache-control']).toContain('no-store');
  const detail = await detailResponse.json() as { content_available: boolean; input_text?: string; output_text?: string };
  expect(detail.content_available).toBe(true);
  expect(detail.input_text).toContain(roughIdea);
  expect(detail.output_text).toContain('Verify persisted Luna history');

  const unsafeLimit = await request.get('/api/v1/codex/runs?limit=101');
  expect(unsafeLimit.status()).toBe(400);
  expect(await unsafeLimit.json()).toMatchObject({ error: { code: 'invalid_request' } });

  await testInfo.attach('luna-history-response.json', {
    body: Buffer.from(JSON.stringify(payload, null, 2)),
    contentType: 'application/json'
  });
  await testInfo.attach('luna-history-settings.png', {
    body: await page.screenshot({ fullPage: true }),
    contentType: 'image/png'
  });
});
