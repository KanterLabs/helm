import { expect, test } from '@playwright/test';

test('shows live Luna steps and opens private input and output', async ({ page }, testInfo) => {
  const startedAt = '2026-09-24T12:00:00Z';
  let listReads = 0;
  let detailReads = 0;
  await page.route('**/api/v1/codex/account*', (route) => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ connected: true, account_type: 'chatgpt', plan_type: 'plus' })
  }));
  await page.route('**/api/v1/codex/runs?*', (route) => {
    listReads += 1;
    const done = listReads > 1;
    return route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify({ data: [{
        id: 'luna-browser-run', feature: 'task_draft', outcome: done ? 'invalid_output' : 'running',
        model: 'gpt-5.6-luna', effort: 'medium', started_at: startedAt,
        ...(done ? { duration_ms: 3000, completed_at: '2026-09-24T12:00:03Z' } : {}),
        steps: [
          { sequence: 1, kind: 'requested', at: startedAt },
          ...(done ? [{ sequence: 2, kind: 'response_completed', at: '2026-09-24T12:00:02Z' }, { sequence: 3, kind: 'validation', at: '2026-09-24T12:00:03Z' }] : [])
        ]
      }], next_cursor: '' })
    });
  });
  await page.route('**/api/v1/codex/runs/luna-browser-run', (route) => {
    detailReads += 1;
    return route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify({ id: 'luna-browser-run', feature: 'task_draft', outcome: 'invalid_output',
        model: 'gpt-5.6-luna', effort: 'medium', started_at: startedAt,
        content_available: true, input_text: 'Browser fixture input', output_text: '{"invalid":"browser fixture output"}',
        input_truncated: false, output_truncated: false })
    });
  });

  await page.goto('/settings');
  const history = page.locator('.luna-history');
  await expect(history.getByRole('heading', { name: 'Recent Luna work' })).toBeVisible();
  await expect(history.getByText('Running', { exact: true })).toBeVisible();
  await expect(history).not.toContainText('Browser fixture input');
  await expect.poll(() => listReads).toBeGreaterThan(1);
  await expect(history.getByText('Invalid output', { exact: true })).toBeVisible();

  await history.locator('.luna-run-summary').click();
  await expect(history.getByText('Browser fixture input')).toBeVisible();
  await expect(history.getByText('{"invalid":"browser fixture output"}')).toBeVisible();
  await expect(history.getByRole('list', { name: 'Luna execution steps' }).locator('li')).toHaveCount(3);
  expect(detailReads).toBeGreaterThan(0);

  const screenshot = testInfo.outputPath('luna-run-history.png');
  await history.screenshot({ path: screenshot });
  await testInfo.attach('luna-run-history.png', { path: screenshot, contentType: 'image/png' });
});
