import { expect, test, type APIRequestContext, type APIResponse, type Locator, type Page } from '@playwright/test';

type Project = { id: string; key: string; name: string; slug: string };
type Column = { id: string; name: string; semantic_state: string; position: number };
type Task = { id: string; key: string; title: string; project_id: string; column_id: string };
type Collection<T> = { data: T[]; next_cursor?: string | null };

const e2eOrigin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;

function mutationHeaders(key = `mobile-contrast-e2e-${crypto.randomUUID()}`): Record<string, string> {
  return {
    Origin: e2eOrigin,
    'Content-Type': 'application/json',
    'Idempotency-Key': key
  };
}

async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

function collectionData<T>(payload: Collection<T> | T[]): T[] {
  return Array.isArray(payload) ? payload : payload.data;
}

function columnFor(columns: Column[], semanticState: string): Column {
  const column = columns.find((item) => item.semantic_state === semanticState);
  expect(column, `the project should have a ${semanticState} column`).toBeTruthy();
  return column as Column;
}

async function createProject(request: APIRequestContext, suffix: string): Promise<Project> {
  return json<Project>(await request.post('/api/v1/projects', {
    data: {
      key: `MC${suffix}`.slice(0, 16),
      name: `Mobile contrast ${suffix}`,
      description: 'Mobile contrast regression fixture.'
    },
    headers: mutationHeaders()
  }), 'create mobile contrast project');
}

async function createBug(request: APIRequestContext, project: Project, column: Column, suffix: string): Promise<Task> {
  return json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: {
      title: `Mobile drawer bug ${suffix}`,
      column_id: column.id,
      priority: 'high',
      kind: 'bug',
      bug: {
        severity: 's2',
        actual_behavior: 'The compact drawer clips its controls.',
        expected_behavior: 'The compact drawer keeps every control usable.',
        reproduction_steps: 'Open this task at a 320px viewport.',
        environment: 'Playwright mobile viewport',
        affected_version: 'beta'
      }
    },
    headers: mutationHeaders()
  }), 'create mobile contrast bug');
}

async function computedContrast(locator: Locator): Promise<number> {
  return locator.evaluate((element: Element) => {
    const style = getComputedStyle(element);
    const parse = (value: string): [number, number, number] => {
      const match = value.match(/rgba?\((\d+)\D+(\d+)\D+(\d+)/);
      if (!match) throw new Error(`Could not parse computed color ${value}`);
      return [Number(match[1]), Number(match[2]), Number(match[3])];
    };
    const luminance = (value: string): number => parse(value)
      .map((channel) => {
        const normalized = channel / 255;
        return normalized <= 0.03928 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4;
      })
      .reduce((sum, channel, index) => sum + channel * [0.2126, 0.7152, 0.0722][index], 0);
    const foreground = luminance(style.color);
    const background = luminance(style.backgroundColor);
    return (Math.max(foreground, background) + 0.05) / (Math.min(foreground, background) + 0.05);
  });
}

async function expectNoViewportOverflow(page: Page): Promise<void> {
  const dimensions = await page.evaluate(() => ({
    documentWidth: document.documentElement.scrollWidth,
    viewportWidth: window.innerWidth,
    overflowers: Array.from(document.querySelectorAll<HTMLElement>('body *'))
      .map((element) => {
        const rect = element.getBoundingClientRect();
        return {
          element: `${element.tagName.toLowerCase()}${element.id ? `#${element.id}` : ''}${Array.from(element.classList).map((name) => `.${name}`).join('')}`,
          left: Math.round(rect.left),
          right: Math.round(rect.right),
          width: Math.round(rect.width),
          scrollWidth: element.scrollWidth
        };
      })
      .filter((item) => item.right > window.innerWidth + 1)
      .slice(0, 12)
  }));
  expect(dimensions.documentWidth, JSON.stringify(dimensions.overflowers, null, 2)).toBeLessThanOrEqual(dimensions.viewportWidth);
}

async function expectDrawerContrast(drawer: Locator): Promise<void> {
  for (const semantic of [
    drawer.locator('.issue-kind-badge'),
    drawer.locator('.severity-badge'),
    drawer.locator('.priority-pill.priority-high'),
    drawer.locator('.bug-resolution-section .complete-button')
  ]) {
    await expect(semantic).toBeVisible();
    expect(await computedContrast(semantic)).toBeGreaterThanOrEqual(4.5);
  }
}

test.describe('mobile layout and semantic contrast regressions', () => {
  test('keeps settings and the task drawer usable at 320px', async ({ page, request }) => {
    test.setTimeout(120_000);
    const status = await json<{ mode?: string }>(await request.get('/api/v1/auth/status'), 'read auth status');
    expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

    const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`.toUpperCase();
    const project = await createProject(request, suffix);
    const columns = collectionData(await json<Collection<Column> | Column[]>(
      await request.get(`/api/v1/projects/${project.id}/columns?limit=20`),
      'list mobile contrast columns'
    ));
    const bug = await createBug(request, project, columnFor(columns, 'ready'), suffix);

    await page.emulateMedia({ colorScheme: 'light' });
    await page.setViewportSize({ width: 320, height: 844 });
    await page.goto('/settings');
    await expect(page.getByRole('heading', { name: 'Settings', exact: true })).toBeVisible();
    await expect(page.locator('#admin-project-select')).toBeVisible();
    await expectNoViewportOverflow(page);

    const adminSection = page.locator('.admin-section');
    const adminBox = await adminSection.boundingBox();
    if (!adminBox) throw new Error('The project administration section should be visible.');
    expect(adminBox.x + adminBox.width).toBeLessThanOrEqual(320);
    const columnName = page.getByLabel('New column name');
    await columnName.fill(`Review ${suffix}`);
    await page.getByRole('button', { name: /Add column/ }).click();
    const confirmation = page.getByRole('alertdialog');
    await expect(confirmation).toBeVisible();
    await confirmation.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(confirmation).toBeHidden();

    await page.goto(`/p/${project.slug}`);
    const card = page.locator('.task-card').filter({ hasText: bug.key });
    const trigger = card.locator('[data-task-trigger]');
    await expect(trigger).toBeVisible();
    await trigger.click();

    const drawer = page.locator('.task-drawer');
    await expect(drawer).toBeVisible();
    const header = drawer.locator('.drawer-header');
    const watch = header.getByRole('button', { name: `Watch task ${bug.key}` });
    const close = header.getByRole('button', { name: 'Close task details', exact: true });
    await expect(watch).toBeVisible();
    await expect(close).toBeVisible();
    const controls = await Promise.all([watch, close].map(async (control) => control.boundingBox()));
    for (const box of controls) {
      if (!box) throw new Error('The drawer header control should have a visible bounding box.');
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(320);
      expect(box.height).toBeGreaterThanOrEqual(44);
    }
    await expectNoViewportOverflow(page);
    await expectDrawerContrast(drawer);

    await watch.click();
    await expect(watch).toHaveAttribute('aria-pressed', 'true');
    await close.click();
    await expect(drawer).toBeHidden();
    await expect(trigger).toBeFocused();

    await page.goto('/settings');
    await expect(page.locator('.theme-options > button').nth(1)).toBeVisible();
    await page.locator('.theme-options > button').nth(1).click();
    await expect(page.locator('.app-shell')).toHaveClass(/dark-mode/);
    await page.goto(`/p/${project.slug}`);
    const darkTrigger = page.locator('.task-card').filter({ hasText: bug.key }).locator('[data-task-trigger]');
    await expect(darkTrigger).toBeVisible();
    await darkTrigger.click();
    const darkDrawer = page.locator('.task-drawer');
    await expect(darkDrawer).toBeVisible();
    await expectDrawerContrast(darkDrawer);
    await expectNoViewportOverflow(page);
    await darkDrawer.getByRole('button', { name: 'Close task details', exact: true }).click();
    await expect(darkDrawer).toBeHidden();
  });
});
