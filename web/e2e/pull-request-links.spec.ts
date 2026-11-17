import { expect, test } from '@playwright/test';
import { currentTask, endpoint, fixture, headers, json, link, links, receipt, type Task } from './support/pullRequestLinks';

// Authored before implementation. Failure modes: provider identity collapses
// hosts; canonical URL variants duplicate or churn task versions; malformed
// input writes data; stale writes overwrite changes; links leak across tasks;
// browser reload loses data; mobile/keyboard controls cannot link/unlink;
// permission/claim checks are bypassed; portable copies omit or misassign PRs.
test.use({ trace: 'on' });

test.describe('task pull request URLs against the real server', () => {

  test('keyboard and mobile linking persists multiple providers and unlinks only the selected PR', async ({ page, request }, info) => {
    const { project, task } = await fixture(request, 'browser');
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`/p/${project.slug}/tasks/${task.key}`);
    await expect(page.getByRole('heading', { name: 'Pull requests', exact: true })).toBeVisible();
    const input = page.getByLabel('Pull request URL', { exact: true });
    const urls = ['https://github.com/synthetic-helm/links/pull/392', 'https://git.synthetic.invalid/synthetic-helm/links/pulls/392'];
    for (const url of urls) {
      await input.fill(url);
      await input.press('Enter');
      await expect(page.locator(`a[href="${url}"]`)).toBeVisible();
    }
    const persisted = await links(request, task);
    expect(persisted.map((item) => item.provider).sort()).toEqual(['gitea', 'github']);
    expect(persisted.map((item) => item.url).sort()).toEqual([...urls].sort());
    await page.reload();
    for (const url of urls) await expect(page.locator(`a[href="${url}"]`)).toBeVisible();
    const external = page.locator(`a[href="${urls[0]}"]`);
    await expect(external).toHaveAttribute('target', '_blank');
    await expect(external).toHaveAttribute('rel', /noopener/);
    await page.getByRole('button', { name: 'Remove pull request GitHub · github.com/synthetic-helm/links #392', exact: true }).click();
    await expect(page.locator(`a[href="${urls[0]}"]`)).toHaveCount(0);
    await expect(page.locator(`a[href="${urls[1]}"]`)).toBeVisible();
    await page.reload();
    expect((await links(request, task)).map((item) => item.url)).toEqual([urls[1]]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
    await receipt(info, 'synthetic-link-persistence', { persisted, afterUnlink: await links(request, task) });
    await info.attach('mobile-pull-request-links.png', { body: await page.locator('section[aria-labelledby="pull-requests-heading"]').screenshot(), contentType: 'image/png' });
  });

  test('canonical duplicates keep identity and version, with host and task isolation', async ({ request }, info) => {
    const f = await fixture(request, 'identity');
    const first = await link(request, f.task, 'https://github.com/synthetic-helm/links/pull/392?utm_source=proof#discussion');
    expect(first.link).toMatchObject({ task_id: f.task.id, provider: 'github', host: 'github.com', repository: 'synthetic-helm/links', number: 392, url: 'https://github.com/synthetic-helm/links/pull/392' });
    expect(first.link.id).toBeTruthy();
    expect(Number.isNaN(Date.parse(first.link.created_at))).toBe(false);
    const duplicate = await link(request, first.task, 'https://github.com/synthetic-helm/links/pull/392/');
    expect(duplicate.link.id).toBe(first.link.id);
    expect(duplicate.task.version).toBe(first.task.version);
    const filesDuplicate = await link(request, duplicate.task, 'https://github.com/synthetic-helm/links/pull/392/files?diff=split#proof');
    expect(filesDuplicate.link.id).toBe(first.link.id);
    expect(filesDuplicate.task.version).toBe(first.task.version);
    const gitea = await link(request, duplicate.task, 'https://one.synthetic.invalid/synthetic-helm/links/pulls/392');
    const secondHost = await link(request, gitea.task, 'https://two.synthetic.invalid/synthetic-helm/links/pulls/392');
    expect(await links(request, f.task)).toHaveLength(3);
    const secondTask = await f.createTask('Another synthetic task sharing a PR');
    const shared = await link(request, secondTask, first.link.url);
    expect(shared.link.id).toBe(first.link.id);
    expect(shared.link.task_id).toBe(secondTask.id);
    const wrongTaskDelete = await request.delete(`${endpoint(secondTask)}/${gitea.link.id}`, { headers: headers(shared.task.version) });
    expect(wrongTaskDelete.status()).toBe(404);
    expect(await links(request, secondTask)).toHaveLength(1);
    expect((await currentTask(request, f.task)).version).toBe(secondHost.task.version);
    await receipt(info, 'canonical-identity', { first, duplicate, scopedHostLinks: await links(request, f.task), shared, wrongTaskDelete: wrongTaskDelete.status() });
  });

  test('invalid URLs and stale writes never mutate links or task data', async ({ request }, info) => {
    const { task } = await fixture(request, 'validation');
    const rejected: Array<{ url: string; status: number }> = [];
    for (const url of [
      '', 'javascript:alert(1)', 'http://github.com/owner/repo/pull/1',
      'https://user:password@github.com/owner/repo/pull/1',
      'https://github.com/owner/repo/issues/1', 'https://github.com/owner/repo/pull/0',
      'https://git.synthetic.invalid/owner/repo/pulls/not-a-number',
      'https://github.com.evil.invalid/owner/repo/pull/1', 'https://github.com/owner/repo/pull/1/../../issues/2'
    ]) {
      const response = await request.post(endpoint(task), { headers: headers(task.version), data: { url } });
      expect(response.status(), url).toBe(400);
      rejected.push({ url, status: response.status() });
    }
    expect(await links(request, task)).toEqual([]);
    expect((await currentTask(request, task)).version).toBe(task.version);
    const added = await link(request, task, 'https://github.com/synthetic-helm/links/pull/393');
    const stalePost = await request.post(endpoint(task), { headers: headers(task.version), data: { url: 'https://github.com/synthetic-helm/links/pull/394' } });
    expect(stalePost.status()).toBe(409);
    const staleDelete = await request.delete(`${endpoint(task)}/${added.link.id}`, { headers: headers(task.version) });
    expect(staleDelete.status()).toBe(409);
    expect(await links(request, task)).toEqual([added.link]);
    expect((await currentTask(request, task)).version).toBe(added.task.version);
    const missingVersion = await request.post(endpoint(task), { headers: headers(), data: { url: 'https://github.com/synthetic-helm/links/pull/395' } });
    expect(missingVersion.status()).toBe(428);
    const deleted = await json<{ task: Task }>(await request.delete(`${endpoint(task)}/${added.link.id}`, { headers: headers(added.task.version) }));
    expect(deleted.task.version).toBeGreaterThan(added.task.version);
    expect(await links(request, task)).toEqual([]);
    await receipt(info, 'validation-and-concurrency', { rejected, stalePost: stalePost.status(), staleDelete: staleDelete.status(), missingVersion: missingVersion.status(), deleted });
  });

  test('portable version 5 copies GitHub and Gitea links without changing older export versions', async ({ request }, info) => {
    const source = await fixture(request, 'portable-source');
    const destination = await fixture(request, 'portable-destination');
    const github = await link(request, source.task, 'https://github.com/synthetic-helm/portable/pull/1');
    const legacyVersions: number[] = [];
    for (const version of [2, 3, 4]) {
      const legacy = await json<{ version: number }>(await request.get(`/api/v1/projects/${source.project.id}/export?version=${version}`));
      expect(legacy.version).toBe(version);
      legacyVersions.push(legacy.version);
    }
    await link(request, github.task, 'https://git.synthetic.invalid/synthetic-helm/portable/pulls/2');
    for (const version of legacyVersions) {
      expect((await request.get(`/api/v1/projects/${source.project.id}/export?version=${version}`)).status()).toBe(400);
    }
    const archive = await json<{ version: number }>(await request.get(`/api/v1/projects/${source.project.id}/export?version=5`));
    expect(archive.version).toBe(5);
    const path = `/api/v1/projects/${destination.project.id}/import?conflict=remap`;
    const before = await json<{ data: Task[] }>(await request.get(`/api/v1/projects/${destination.project.id}/tasks`));
    await json(await request.post(`${path}&dry_run=true`, { headers: headers(), data: archive }));
    const afterReview = await json<{ data: Task[] }>(await request.get(`/api/v1/projects/${destination.project.id}/tasks`));
    expect(afterReview.data).toHaveLength(before.data.length);
    await json(await request.post(path, { headers: headers(), data: archive }));
    const copied = (await json<{ data: Task[] }>(await request.get(`/api/v1/projects/${destination.project.id}/tasks`))).data.find((item) => item.title === source.task.title)!;
    expect(copied).toBeTruthy();
    const copiedLinks = await links(request, copied);
    expect(copiedLinks.map((item) => item.url).sort()).toEqual((await links(request, source.task)).map((item) => item.url).sort());
    for (const item of copiedLinks) expect(item.task_id).toBe(copied.id);
    await receipt(info, 'portable-pr-roundtrip', { version: archive.version, legacyVersions, reviewTaskCount: afterReview.data.length, copiedLinks });
  });
});

