import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test';

// Proves docs/TC337_TICKETS_UX_FAILURES.md against a real Helm process,
// database and browser. Webhook URLs are one-time secrets: this file keeps
// no trace, video or unmasked failure screenshot, and every attached
// screenshot masks the reveal and the curl example.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

const baseURL = process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080';
const origin = new URL(baseURL).origin;
const jsonHeaders = { Origin: origin, 'Content-Type': 'application/json' };
const adminPaths = /\/api\/v1\/(ticket-webhooks|email-inboxes|email-intake|public-endpoints)\b/;

function manager(page: Page): Locator {
  return page.getByRole('dialog', { name: 'Connect apps' });
}

function tab(page: Page, name: 'Email' | 'Webhooks' | 'Public access'): Locator {
  return manager(page).getByRole('tab', { name: new RegExp(`^${name}`) });
}

async function shot(page: Page, info: TestInfo, name: string, target?: Locator) {
  const mask = [page.locator('[data-webhook-secret-reveal]'), page.locator('[data-webhook-curl]')];
  const body = target ? await target.screenshot({ mask }) : await page.screenshot({ mask });
  await info.attach(name, { contentType: 'image/png', body });
}

async function noHorizontalOverflow(page: Page) {
  const widths = await page.evaluate(() => {
    const dialog = document.querySelector<HTMLElement>('.ticket-integrations');
    return {
      page: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      dialog: dialog ? dialog.scrollWidth - dialog.clientWidth : 0
    };
  });
  expect(widths.page, 'page scrolls sideways').toBeLessThanOrEqual(0);
  expect(widths.dialog, 'manager scrolls sideways').toBeLessThanOrEqual(0);
}

async function activeInsideManager(page: Page): Promise<boolean> {
  return page.evaluate(() => Boolean(document.activeElement?.closest('.ticket-integrations')));
}

// Synthetic inbox data so the Email tab has content on servers without
// Cloudflare email. Creation still goes to the real API elsewhere.
async function syntheticInboxes(page: Page) {
  await page.route('**/api/v1/email-inboxes', async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    await route.fulfill({ json: { email_on: true, data: [
      { id: 'inbox-synthetic-1', name: 'Homelab alerts', address: 'helm-alerts+homelab-alerts-abcd23@example.test', assignee_name: 'Synthetic Admin', received_count: 3, last_received_at: '2026-10-30T09:00:00Z', created_at: '2026-10-01T00:00:00Z' },
      { id: 'inbox-synthetic-2', name: 'Old backups', address: 'helm-alerts+old-backups-efgh45@example.test', received_count: 0, disabled_at: '2026-10-20T00:00:00Z', created_at: '2026-09-01T00:00:00Z' }
    ] } });
  });
}

test('The queue stays first and Connect apps opens a calm, tabbed manager', async ({ page }, testInfo) => {
  test.setTimeout(120_000);
  await syntheticInboxes(page);
  for (const [width, height] of [[1440, 900], [390, 844], [320, 640]] as const) {
    for (const scheme of ['light', 'dark'] as const) {
      await page.setViewportSize({ width, height });
      await page.emulateMedia({ colorScheme: scheme });
      await page.goto('/tickets');
      const label = `${width}-${scheme}`;
      const queues = page.getByRole('tablist', { name: 'Ticket queues' });
      await expect(queues).toBeVisible();
      // One concise heading and one primary action; Connections is quiet.
      const heading = page.locator('.tickets-heading');
      await expect(heading.locator('.button.primary')).toHaveCount(1);
      await expect(heading.getByRole('button', { name: '＋ New ticket' })).toBeVisible();
      const entry = page.getByRole('button', { name: /Connect apps/ });
      await expect(entry).toHaveAttribute('aria-haspopup', 'dialog');
      await expect(heading.locator('.breadcrumbs')).toHaveCount(0);
      const before = await queues.boundingBox();
      expect(before, 'queue tabs are rendered').toBeTruthy();
      expect(before!.y + before!.height, `queue tabs start above the fold at ${label}`).toBeLessThan(height);
      await expect(page.getByRole('searchbox', { name: 'Search tickets' })).toBeInViewport();
      await noHorizontalOverflow(page);
      await shot(page, testInfo, `queue-${label}.png`);

      await entry.click();
      const dialog = manager(page);
      await expect(dialog).toBeVisible();
      await expect(entry).toHaveAttribute('aria-expanded', 'true');
      // The manager is a layer: the queue does not move underneath it.
      expect((await queues.boundingBox())!.y).toBe(before!.y);
      await expect(dialog.getByRole('tab')).toHaveCount(3);
      for (const name of ['Email', 'Webhooks', 'Public access'] as const) {
        await tab(page, name).click();
        await expect(tab(page, name)).toHaveAttribute('aria-selected', 'true');
        await expect(dialog.getByRole('tabpanel')).toHaveCount(1);
        await expect(dialog.getByRole('tabpanel')).toBeVisible();
        // Lists first; creation forms and the API guide wait to be asked for.
        await expect(dialog.getByRole('form', { name: 'New inbox' })).toHaveCount(0);
        await expect(dialog.getByRole('form', { name: 'New webhook' })).toHaveCount(0);
        await expect(dialog.getByRole('heading', { name: 'How to send a ticket' })).toBeHidden();
        await noHorizontalOverflow(page);
        await shot(page, testInfo, `manager-${name.toLowerCase().replace(' ', '-')}-${label}.png`);
      }
      // Email: list, copy, mailto, menu actions and retired list stay reachable.
      await tab(page, 'Email').click();
      const inbox = dialog.locator('[data-inbox="Homelab alerts"]');
      await expect(inbox.locator('[data-inbox-address]')).toHaveText('helm-alerts+homelab-alerts-abcd23@example.test');
      await expect(inbox.getByRole('button', { name: 'Copy' })).toBeVisible();
      await expect(inbox.locator('[data-inbox-mailto]')).toHaveAttribute('href', /^mailto:helm-alerts\+homelab-alerts-abcd23@example\.test\?subject=/);
      await inbox.getByLabel('More actions for Homelab alerts').click();
      await expect(inbox.getByRole('button', { name: 'Replace address' })).toBeVisible();
      await expect(inbox.getByRole('button', { name: 'Turn off' })).toBeVisible();
      await expect(dialog.getByText('Turned off (1)')).toBeVisible();
      await dialog.getByRole('button', { name: 'New inbox' }).click();
      await expect(dialog.getByRole('form', { name: 'New inbox' })).toBeVisible();
      await expect(dialog.getByLabel('Assign to me')).toBeChecked();
      await noHorizontalOverflow(page);
      await shot(page, testInfo, `manager-email-create-${label}.png`, dialog);
      await dialog.getByRole('button', { name: 'Cancel' }).click();
      await dialog.getByRole('button', { name: 'Close' }).click();
      await expect(dialog).toHaveCount(0);
      await expect(entry).toBeFocused();
    }
  }
});

test('Manager keyboard, focus, drafts and the one-time webhook URL are guarded', async ({ page, request }, testInfo) => {
  test.setTimeout(120_000);
  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/tickets');
  const entry = page.getByRole('button', { name: /Connect apps/ });
  await entry.focus();
  await page.keyboard.press('Enter');
  const dialog = manager(page);
  await expect(dialog).toBeVisible();
  // Focus lands on the selected tab; arrows, Home and End move selection.
  const selected = dialog.getByRole('tab', { selected: true });
  await expect(selected).toBeFocused();
  await tab(page, 'Email').focus();
  await page.keyboard.press('ArrowRight');
  await expect(tab(page, 'Webhooks')).toBeFocused();
  await expect(tab(page, 'Webhooks')).toHaveAttribute('aria-selected', 'true');
  await expect(dialog.getByRole('tabpanel')).toHaveAttribute('aria-labelledby', (await tab(page, 'Webhooks').getAttribute('id')) as string);
  await page.keyboard.press('End');
  await expect(tab(page, 'Public access')).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Home');
  await expect(tab(page, 'Email')).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('ArrowLeft');
  await expect(tab(page, 'Public access')).toBeFocused();
  // "/" does not jump to the ticket search behind the modal.
  await page.keyboard.press('/');
  expect(await activeInsideManager(page)).toBe(true);
  // Tab cycles inside the manager in both directions.
  for (let index = 0; index < 40; index += 1) {
    await page.keyboard.press(index % 5 === 4 ? 'Shift+Tab' : 'Tab');
    expect(await activeInsideManager(page), `focus stays inside after ${index + 1} presses`).toBe(true);
  }

  // A webhook name draft survives tab changes and an accidental close.
  await tab(page, 'Webhooks').click();
  await dialog.getByRole('button', { name: 'New webhook' }).click();
  const form = dialog.getByRole('form', { name: 'New webhook' });
  await expect(form).toBeVisible();
  await expect(form.getByLabel('Webhook format')).toHaveValue('generic');
  await expect(form.getByLabel('Assign new tickets to me')).toBeChecked();
  await form.getByLabel('Webhook name', { exact: true }).fill(`UX draft ${runID}`);
  await tab(page, 'Email').click();
  await tab(page, 'Webhooks').click();
  await expect(form.getByLabel('Webhook name', { exact: true })).toHaveValue(`UX draft ${runID}`);
  await page.keyboard.press('Escape');
  const confirm = page.getByRole('alertdialog');
  await expect(confirm).toContainText('unsaved');
  await confirm.getByRole('button', { name: 'Keep editing' }).click();
  await expect(confirm).toHaveCount(0);
  await expect(dialog).toBeVisible();
  expect(await activeInsideManager(page)).toBe(true);
  await expect(form.getByLabel('Webhook name', { exact: true })).toHaveValue(`UX draft ${runID}`);

  // Creating reveals the URL once; tabs and close attempts keep it.
  await form.getByLabel('Webhook format').selectOption('coolify');
  await form.getByRole('button', { name: 'Create webhook URL' }).click();
  const reveal = dialog.locator('[data-webhook-secret-reveal]');
  await expect(reveal).toContainText('will not be shown again');
  await expect(reveal).toContainText('Coolify notifications');
  const url = await reveal.locator('input').inputValue();
  expect(url).toContain('/api/v1/hooks/tickets/hk_');
  await expect(reveal.locator('input')).toBeFocused();
  await expect(dialog.getByRole('form', { name: 'New webhook' })).toHaveCount(0);
  const row = dialog.locator(`[data-webhook-name="UX draft ${runID}"]`);
  await expect(row).toContainText('Coolify');
  await tab(page, 'Public access').click();
  await expect(tab(page, 'Webhooks')).toContainText('Copy URL');
  await tab(page, 'Webhooks').click();
  await expect(reveal.locator('input')).toHaveValue(url);
  await dialog.getByRole('button', { name: 'How to send' }).click();
  await expect(dialog.getByRole('heading', { name: 'How to send a ticket' })).toBeVisible();
  await expect(dialog.locator('[data-webhook-curl]')).toContainText(url);
  // Long content scrolls inside the manager, which stays in the viewport.
  const box = (await dialog.boundingBox())!;
  expect(box.y + box.height).toBeLessThanOrEqual(900);
  await dialog.getByRole('link', { name: '/openapi.json' }).scrollIntoViewIfNeeded();
  await expect(dialog.getByRole('link', { name: '/openapi.json' })).toBeInViewport();
  // Escape closes an open row menu first, not the manager.
  await row.getByLabel(`More actions for UX draft ${runID}`).click();
  await expect(row.getByRole('button', { name: 'Rotate' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(row.getByRole('button', { name: 'Rotate' })).toBeHidden();
  await expect(page.getByRole('alertdialog')).toHaveCount(0);
  await shot(page, testInfo, 'manager-webhook-reveal-masked.png', dialog);
  await dialog.getByRole('button', { name: 'Close' }).click();
  await expect(confirm).toContainText('shown only once');
  await confirm.getByRole('button', { name: 'Keep open' }).click();
  await expect(reveal.locator('input')).toHaveValue(url);
  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + location.href);
  expect(stored).not.toContain('hk_');
  await reveal.getByRole('button', { name: 'I saved it' }).click();
  await expect(reveal).toHaveCount(0);
  await expect(dialog.locator('[data-webhook-curl]')).not.toContainText('hk_');

  // Help opens over the manager; Escape closes help only and focus returns.
  await tab(page, 'Email').click();
  const learn = dialog.locator('[data-email-inboxes]').getByRole('button', { name: 'Learn more' });
  await learn.click();
  await expect(page.locator('[data-help-drawer]')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('[data-help-drawer]')).toHaveCount(0);
  await expect(dialog).toBeVisible();
  await expect(learn).toBeFocused();

  // Nothing pending: Escape closes straight away and focus returns.
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(entry).toBeFocused();
  const hooks = await (await request.get('/api/v1/ticket-webhooks')).json() as { data: { id: string; name: string }[] };
  const created = hooks.data.find((hook) => hook.name === `UX draft ${runID}`);
  expect(created).toBeTruthy();
  expect((await request.delete(`/api/v1/ticket-webhooks/${created!.id}`, { headers: jsonHeaders })).ok()).toBe(true);
});

test('Deep link keeps queue state, and non-admins get a read-only guide without admin requests', async ({ page, request }, testInfo) => {
  test.setTimeout(90_000);
  const title = `Connections deep link ${Date.now().toString(36)}`;
  const created = await (await request.post('/api/v1/tickets', { data: { title }, headers: { ...jsonHeaders, 'Idempotency-Key': crypto.randomUUID() } })).json() as { key: string };
  expect(created.key).toBeTruthy();
  const publicView = await (await request.get('/api/v1/public-endpoints')).json() as { active?: unknown; connector: { state: string } };
  const expected = publicView.active && publicView.connector.state === 'connected' ? 'Email' : 'Public access';
  await page.goto(`/tickets?queue=needs_triage&ticket=${created.key}&connect=1`);
  await expect(manager(page)).toBeVisible();
  await expect(tab(page, expected)).toHaveAttribute('aria-selected', 'true');
  await expect(page).toHaveURL(new RegExp(`/tickets\\?queue=needs_triage&ticket=${created.key}$`));
  await manager(page).getByRole('button', { name: 'Close' }).click();
  await expect(page.locator('.ticket-detail').getByRole('heading', { name: title })).toBeVisible();
  await expect(page.getByRole('tab', { name: /Needs triage/ })).toHaveAttribute('aria-selected', 'true');

  // A non-admin sees why, and the guide, but nothing admin-only.
  const admin: string[] = [];
  page.on('request', (item) => { if (adminPaths.test(new URL(item.url()).pathname)) admin.push(item.url()); });
  await page.route('**/api/v1/auth/status', async (route) => {
    const real = await (await route.fetch()).json();
    const member = { ...(real.user || real.actor || {}), admin: false };
    await route.fulfill({ json: { ...real, actor: member, user: member } });
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/tickets?connect=1');
  const dialog = manager(page);
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('tab')).toHaveCount(0);
  await expect(dialog).toContainText('Ask a workspace administrator');
  await expect(dialog.getByRole('button', { name: 'New webhook' })).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'New inbox' })).toHaveCount(0);
  await dialog.getByRole('button', { name: 'How to send' }).click();
  await expect(dialog.getByRole('heading', { name: 'How to send a ticket' })).toBeVisible();
  await expect(dialog.locator('[data-webhook-curl]')).toContainText('/api/v1/hooks/tickets/<secret>');
  await noHorizontalOverflow(page);
  await shot(page, testInfo, 'manager-nonadmin-390.png');
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(page.locator('[data-public-pill]')).toHaveCount(0);
  expect(admin, 'non-admins make no admin connection requests').toEqual([]);
});
