import { expect, test, type Page, type Route } from '@playwright/test';

const currentSha = '0123456789abcdef0123456789abcdef01234567';
const targetSha = 'abcdef0123456789abcdef0123456789abcdef01';

type BetaFixture = {
  enabled: boolean;
  buildReads: number;
  jobReads: number;
  switchCalls: string[];
};

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}

function buildPayload(state: BetaFixture) {
  const switched = state.switchCalls.length > 0 && state.buildReads > 1;
  return {
    enabled: state.enabled,
    current_sha: switched ? targetSha : currentSha,
    builds: [
      { sha: currentSha, ref: 'refs/heads/beta', current: !switched },
      { sha: targetSha, ref: 'refs/heads/feature/compact-switcher', current: switched }
    ]
  };
}

async function installRoutes(page: Page, state: BetaFixture) {
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const method = request.method();
    const url = new URL(request.url());
    const pathname = decodeURIComponent(url.pathname);
    const projectId = 'project-beta-ui';
    if (pathname === '/api/v1/auth/status') {
      await json(route, {
        mode: 'disabled',
        configured: true,
        setup_required: false,
        authenticated: true,
        user: { id: 'beta-owner', kind: 'human', name: 'Beta owner', admin: true },
        actor: { id: 'beta-owner', kind: 'human', name: 'Beta owner', admin: true }
      });
      return;
    }
    if (pathname === '/api/v1/admin/beta/builds' && method === 'GET') {
      state.buildReads += 1;
      await json(route, buildPayload(state));
      return;
    }
    if (pathname === '/api/v1/admin/beta/switch' && method === 'POST') {
      const input = JSON.parse(request.postData() || '{}') as { sha?: string };
      state.switchCalls.push(input.sha || '');
      await json(route, { enabled: true, job: { id: '0123456789abcdef0123456789abcdef', target_sha: input.sha, state: 'queued' } }, 202);
      return;
    }
    if (pathname === '/api/v1/admin/beta/switches/0123456789abcdef0123456789abcdef' && method === 'GET') {
      await new Promise((resolve) => setTimeout(resolve, 250));
      state.jobReads += 1;
      await json(route, {
        enabled: true,
        job: {
          id: '0123456789abcdef0123456789abcdef',
          target_sha: targetSha,
          state: state.jobReads > 1 ? 'succeeded' : 'running'
        }
      });
      return;
    }
    if (pathname === '/api/v1/projects' && method === 'GET') {
      await json(route, { data: [{ id: projectId, key: 'BETA', slug: 'beta-ui', name: 'Beta UI', description: 'Beta UI fixture', color: '#6d5efc', favorite: true }], next_cursor: null });
      return;
    }
    if (pathname === `/api/v1/projects/${projectId}/columns` && method === 'GET') {
      await json(route, { data: [{ id: 'column-beta', project_id: projectId, name: 'Backlog', semantic_state: 'backlog', position: 0 }], next_cursor: null });
      return;
    }
    if (pathname === `/api/v1/projects/${projectId}/labels` && method === 'GET') {
      await json(route, { data: [], next_cursor: null });
      return;
    }
    if (pathname === `/api/v1/projects/${projectId}/tasks` && method === 'GET') {
      await json(route, { data: [], next_cursor: null });
      return;
    }
    if (pathname === '/api/v1/issues' && method === 'GET') {
      await json(route, { data: [], next_cursor: null });
      return;
    }
    if (pathname === '/api/v1/issues/metrics' && method === 'GET') {
      await json(route, { open: 0, untriaged: 0, severe: 0, reopened: 0 });
      return;
    }
    if (pathname === '/api/v1/sidebar-counts' && method === 'GET') {
      await json(route, { issues: 0, my_work: 0 });
      return;
    }
    if (pathname === '/api/v1/events' && method === 'GET') {
      await json(route, { data: [], next_cursor: null });
      return;
    }
    if (pathname.includes('/releases') && method === 'GET') {
      await json(route, { data: [], next_cursor: null });
      return;
    }
    await json(route, { data: [], next_cursor: null });
  });
}

async function openFixture(page: Page, enabled = true) {
  const state: BetaFixture = { enabled, buildReads: 0, jobReads: 0, switchCalls: [] };
  await installRoutes(page, state);
  await page.goto('/');
  await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible();
  await expect(page.locator('section.board')).toBeVisible();
  return state;
}

test.describe('owner beta build switcher', () => {
  test('stays hidden when the optional beta response is disabled', async ({ page }) => {
    await openFixture(page, false);
    await expect(page.locator('[data-beta-switcher]')).toHaveCount(0);
  });

  test('exposes a keyboard-accessible retained build menu and restores focus', async ({ page }) => {
    await openFixture(page);
    const trigger = page.locator('[data-beta-switcher] > button');
    await expect(trigger).toHaveAttribute('aria-haspopup', 'menu');
    await expect(trigger).toHaveAttribute('aria-controls', 'beta-build-menu');
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    await trigger.click();
    const menu = page.getByRole('menu', { name: 'Retained beta builds' });
    await expect(menu).toBeVisible();
    await expect(trigger).toHaveAttribute('aria-expanded', 'true');
    await expect(menu.getByRole('menuitem', { name: /feature\/compact-switcher/ })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(menu).toBeHidden();
    await expect(trigger).toBeFocused();
    await trigger.click();
    await page.locator('main.content').click({ position: { x: 8, y: 8 } });
    await expect(menu).toBeHidden();
    await expect(trigger).toBeFocused();
  });

  test('confirms, switches, polls restart status, and announces completion', async ({ page }) => {
    const state = await openFixture(page);
    const trigger = page.locator('[data-beta-switcher] > button');
    await trigger.click();
    await page.getByRole('menuitem', { name: /feature\/compact-switcher/ }).click();
    await expect(page.getByRole('button', { name: 'Switch beta', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Switch beta', exact: true }).click();
    await expect(page.locator('.beta-switch-status')).toContainText(/Switching|Restarting beta/);
    await expect.poll(() => state.switchCalls.length).toBe(1);
    await expect(trigger).toContainText('abcdef0');
    await expect(page.locator('.sr-only[aria-live="polite"]')).toContainText('Beta restarted on abcdef0');
  });

  test('fits a 320px viewport without horizontal overflow', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 700 });
    await openFixture(page);
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    const trigger = page.locator('[data-beta-switcher] > button');
    await expect(trigger).toBeVisible();
    await trigger.click();
    const menu = page.getByRole('menu', { name: 'Retained beta builds' });
    await expect(menu).toBeVisible();
    await expect.poll(() => menu.boundingBox().then((box) => (box?.right || 0) <= 320)).toBe(true);
  });
});
