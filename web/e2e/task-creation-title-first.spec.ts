import { expect, test, type Dialog, type Page } from '@playwright/test';
import { columns, evidence, headers, json, project, suffix, tasks, type Project, type Task } from './creation-safety-helpers';

// Failure inventory written before implementation: docs/TC318_TASK_CREATION_FAILURES.md.
// Retry, lost-response and validation safety are reused from creation-safety.spec.ts.
// These flows use real HTTP, so keep the browser's real registration lifecycle.
test.use({ serviceWorkers: 'allow' });

type Release = { id: string; name: string; status: string };
type Created = Task & { key: string; priority: string; release_id?: string | null };

const retiredAssist = /^\/api\/v1\/(codex\/|projects\/[^/]+\/task-draft)/;

// Records only requests made after the caller starts watching, so unrelated
// page bootstrap reads cannot mask or fake an ordinary-form regression.
function assistTraffic(page: Page) {
  const seen: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (retiredAssist.test(path)) seen.push(`${request.method()} ${path}`);
  });
  return seen;
}

async function readyColumn(request: Parameters<typeof columns>[0], destination: Project) {
  return (await columns(request, destination)).find((column) => column.semantic_state === 'ready')!;
}

async function expectOrdinaryForm(page: Page, destination: Project, columnId: string) {
  const dialog = page.getByRole('dialog', { name: 'Create a task', exact: true });
  await expect(dialog).toBeVisible();
  await expect(page.locator('.toast.error')).toHaveCount(0);
  await expect(dialog.getByLabel('Task title', { exact: true })).toBeFocused();
  await expect(dialog.getByRole('combobox', { name: 'Project', exact: true })).toHaveValue(destination.id);
  await expect(dialog.getByRole('combobox', { name: 'Column', exact: true })).toHaveValue(columnId);
  await expect(dialog.getByLabel('Rough idea')).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: /Luna|Connect Codex/ })).toHaveCount(0);
  // Project option names are user data, so assert retired copy outside the destination picker.
  await expect(dialog.getByText(/Rough idea|Plan it with Luna|Assist with Luna|Connect (your )?Codex|ChatGPT/)).toHaveCount(0);
  await expect(dialog.getByRole('heading', { name: /Luna|Codex/ })).toHaveCount(0);
  return dialog;
}

test.describe('title-first task creation', () => {
  test('one board action opens a title-first form and keyboard creation needs no AI or account detour', async ({ page, request }, info) => {
    const destination = await project(request);
    const ready = await readyColumn(request, destination);
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/p/${destination.slug}`);
    await expect(page.getByRole('heading', { name: destination.name, exact: true })).toBeVisible();

    const createActions = page.getByRole('button', { name: /^(New task|Create task)$/ });
    await expect(createActions).toHaveCount(1);
    const trigger = page.getByRole('button', { name: 'New task', exact: true });
    const readyLane = page.locator('.board-column').filter({ has: page.getByRole('heading', { name: 'Ready', exact: true }) });
    await expect(readyLane.getByRole('button', { name: /Add task$/ })).toBeVisible();

    const traffic = assistTraffic(page);
    await trigger.click();
    const dialog = await expectOrdinaryForm(page, destination, ready.id);
    const title = `Title first ${suffix()}`;
    await page.keyboard.type(title);
    const posted = page.waitForResponse((response) => response.url().endsWith(`/api/v1/projects/${destination.id}/tasks`) && response.request().method() === 'POST');
    await page.keyboard.press('Enter');
    expect((await posted).ok()).toBeTruthy();
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();

    const saved = (await tasks(request, destination)).filter((task) => task.title === title) as Created[];
    expect(saved).toHaveLength(1);
    expect(saved[0].column_id).toBe(ready.id);
    expect(saved[0].priority).toBe('normal');
    expect(traffic).toEqual([]);
    await evidence(page, info, 'title-first-keyboard-create', { createActions: 1, saved: saved.map(({ title, column_id, priority }) => ({ title, column_id, priority })), retiredAssistRequests: traffic });
  });

  test('an unsaved manual draft survives accidental dismissal and is discarded only on confirmation', async ({ page, request }, info) => {
    const destination = await project(request);
    const ready = await readyColumn(request, destination);
    await page.goto(`/p/${destination.slug}`);
    const trigger = page.getByRole('button', { name: 'New task', exact: true });
    const prompts: string[] = [];

    await trigger.click();
    let dialog = await expectOrdinaryForm(page, destination, ready.id);
    // An empty form must close without asking; record any prompt as a failure.
    const unexpected = (prompt: Dialog) => { prompts.push(prompt.message()); void prompt.dismiss(); };
    page.on('dialog', unexpected);
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    page.off('dialog', unexpected);
    expect(prompts).toEqual([]);
    await expect(trigger).toBeFocused();

    await trigger.click();
    dialog = await expectOrdinaryForm(page, destination, ready.id);
    const title = `Unsaved manual draft ${suffix()}`;
    await page.keyboard.type(title);
    await dialog.getByLabel('Description', { exact: false }).fill('Context typed by hand.');
    for (const dismiss of [
      () => page.keyboard.press('Escape'),
      () => page.locator('.modal-backdrop').dispatchEvent('click'),
      () => dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    ]) {
      // The native confirm blocks the triggering input, so answer it as it opens.
      const before = prompts.length;
      const keep = (prompt: Dialog) => { prompts.push(prompt.message()); void prompt.dismiss(); };
      page.once('dialog', keep);
      await dismiss();
      expect(prompts).toHaveLength(before + 1);
      await expect(dialog).toBeVisible();
      await expect(dialog.getByLabel('Task title', { exact: true })).toHaveValue(title);
      await expect(dialog.getByLabel('Description', { exact: false })).toHaveValue('Context typed by hand.');
    }
    expect(prompts).toHaveLength(3);
    await evidence(page, info, 'manual-draft-kept', { prompts, persisted: (await tasks(request, destination)).length });

    page.once('dialog', (prompt) => { prompts.push(prompt.message()); void prompt.accept(); });
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
    expect(await tasks(request, destination)).toHaveLength(0);
    await trigger.click();
    dialog = await expectOrdinaryForm(page, destination, ready.id);
    await expect(dialog.getByLabel('Task title', { exact: true })).toHaveValue('');
    await evidence(page, info, 'manual-draft-discarded', { prompts, persisted: 0 });
  });

});

test.describe('title-first task creation on a touch phone', () => {
  test.use({ hasTouch: true });

  test('phone child creation keeps parent and Focus, and a project change clears both before creating', async ({ page, request }, info) => {
    const home = await project(request);
    const other = await project(request);
    const homeReady = await readyColumn(request, home);
    const otherReady = await readyColumn(request, other);
    const focus = await json<Release>(await request.post(`/api/v1/projects/${home.id}/releases`, { headers: headers(), data: { name: `Focus ${suffix()}` } }));
    const parent = await json<Created>(await request.post(`/api/v1/projects/${home.id}/tasks`, {
      headers: headers(), data: { title: `Parent ${suffix()}`, column_id: homeReady.id, priority: 'normal', release_id: focus.id }
    }));
    await page.setViewportSize({ width: 390, height: 844 });
    const traffic = assistTraffic(page);

    await page.goto(`/p/${home.slug}`);
    await page.getByRole('button', { name: 'Create new', exact: true }).click();
    await page.getByRole('menuitem', { name: /Task/ }).click();
    let dialog = await expectOrdinaryForm(page, home, homeReady.id);
    const box = await dialog.boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(391);
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).tap();
    await expect(dialog).toBeHidden();

    await page.goto(`/p/${home.slug}/tasks/${parent.key}`);
    const drawer = page.locator('.task-drawer');
    await drawer.getByRole('button', { name: 'Create child', exact: true }).tap();
    dialog = await expectOrdinaryForm(page, home, homeReady.id);
    await expect(dialog.getByText(`child of ${parent.key}`)).toBeVisible();
    await expect(dialog.getByLabel('Focus', { exact: true })).toHaveValue(focus.id);
    const childTitle = `Child ${suffix()}`;
    await page.keyboard.type(childTitle);
    await dialog.getByRole('button', { name: 'Create task', exact: true }).tap();
    await expect(dialog).toBeHidden();
    const child = (await tasks(request, home)).find((task) => task.title === childTitle) as Created;
    expect(child.release_id).toBe(focus.id);
    const childParent = await json<{ parent: { id: string } | null }>(await request.get(`/api/v1/tasks/${child.id}/parent`));
    expect(childParent.parent?.id).toBe(parent.id);
    await evidence(page, info, 'phone-child-parent-focus', { parent: parent.key, child: { column_id: child.column_id, release_id: child.release_id, parent_id: childParent.parent?.id } });

    await drawer.getByRole('button', { name: 'Create child', exact: true }).tap();
    dialog = await expectOrdinaryForm(page, home, homeReady.id);
    const movedTitle = `Moved project ${suffix()}`;
    await page.keyboard.type(movedTitle);
    await dialog.getByRole('combobox', { name: 'Project', exact: true }).selectOption(other.id);
    await expect(dialog.getByRole('combobox', { name: 'Column', exact: true })).toHaveValue(otherReady.id);
    await expect(dialog.getByText(`child of ${parent.key}`)).toHaveCount(0);
    await expect(dialog.getByLabel('Focus', { exact: true })).toHaveValue('');
    await expect(dialog.getByLabel('Task title', { exact: true })).toHaveValue(movedTitle);
    await dialog.getByRole('button', { name: 'Create task', exact: true }).tap();
    await expect(dialog).toBeHidden();
    expect((await tasks(request, home)).filter((task) => task.title === movedTitle)).toHaveLength(0);
    const moved = (await tasks(request, other)).filter((task) => task.title === movedTitle) as Created[];
    expect(moved).toHaveLength(1);
    expect(moved[0].column_id).toBe(otherReady.id);
    expect((await json<{ parent: unknown }>(await request.get(`/api/v1/tasks/${moved[0].id}/parent`))).parent).toBeNull();
    expect(traffic).toEqual([]);
    await evidence(page, info, 'phone-project-change-clears-parent', { moved: moved.map(({ project_id, column_id }) => ({ project_id, column_id })), retiredAssistRequests: traffic });
  });
});
