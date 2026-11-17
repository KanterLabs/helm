import { expect, test } from '@playwright/test';
import { currentTask, endpoint, fixture, headers, json, links, receipt, type Task } from './support/pullRequestLinks';
import { createAgent, issueToken, requireDisabledAuth } from './support/agentTokens';

// Credentials remain in memory only: API traces contain request headers.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

test.describe('PR link authorization with real bearer credentials', () => {
  test('enforces read/write scopes and project ceilings', async ({ request }, info) => {
    await requireDisabledAuth(request);
    const allowed = await fixture(request, 'allowed');
    const outside = await fixture(request, 'outside');
    const agent = await createAgent(request, 'Synthetic PR authorization agent');
    const readOnly = await issueToken(request, agent.id, { name: 'Synthetic PR reader', scopes: ['projects:read', 'tasks:read'], project_ids: [allowed.project.id] });
    const writeOnly = await issueToken(request, agent.id, { name: 'Synthetic PR writer', scopes: ['tasks:write'], project_ids: [allowed.project.id] });
    const auth = (token: string, task: Task) => ({ ...headers(task.version), Authorization: `Bearer ${token}` });
    const statuses = {
      scopedRead: (await request.get(endpoint(allowed.task), { headers: auth(readOnly.token, allowed.task) })).status(),
      outsideRead: (await request.get(endpoint(outside.task), { headers: auth(readOnly.token, outside.task) })).status(),
      writeWithoutRead: (await request.get(endpoint(allowed.task), { headers: auth(writeOnly.token, allowed.task) })).status(),
      readOnlyWrite: (await request.post(endpoint(allowed.task), { headers: auth(readOnly.token, allowed.task), data: { url: 'https://github.com/synthetic-helm/auth/pull/1' } })).status(),
      outsideWrite: (await request.post(endpoint(outside.task), { headers: auth(writeOnly.token, outside.task), data: { url: 'https://github.com/synthetic-helm/auth/pull/2' } })).status()
    };
    expect(statuses).toEqual({ scopedRead: 200, outsideRead: 403, writeWithoutRead: 403, readOnlyWrite: 403, outsideWrite: 403 });
    expect(await links(request, allowed.task)).toEqual([]);
    expect(await links(request, outside.task)).toEqual([]);
    await receipt(info, 'scope-enforcement-statuses', statuses);
  });

  test('foreign claims reject both link and unlink while the claim owner can mutate', async ({ request }, info) => {
    await requireDisabledAuth(request);
    const { project, task } = await fixture(request, 'claim');
    const owner = await createAgent(request, 'Synthetic PR claim owner');
    const foreign = await createAgent(request, 'Synthetic foreign PR writer');
    const grant = { scopes: ['tasks:read', 'tasks:write', 'tasks:claim'], project_ids: [project.id] };
    const ownerToken = await issueToken(request, owner.id, { ...grant, name: 'Synthetic owner token' });
    const foreignToken = await issueToken(request, foreign.id, { ...grant, name: 'Synthetic foreign token' });
    const auth = (token: string, version: number) => ({ ...headers(version), Authorization: `Bearer ${token}` });
    const claimed = await json<Task>(await request.post(`/api/v1/tasks/${task.id}/claim`, {
      headers: auth(ownerToken.token, task.version), data: { lease_seconds: 3600 }
    }));
    const url = 'https://github.com/synthetic-helm/claim/pull/1';
    const deniedPost = await request.post(endpoint(task), { headers: auth(foreignToken.token, claimed.version), data: { url } });
    expect(deniedPost.status()).toBe(409);
    expect((await currentTask(request, task)).version).toBe(claimed.version);
    const owned = await json<{ link: { id: string }; task: Task }>(await request.post(endpoint(task), {
      headers: auth(ownerToken.token, claimed.version), data: { url }
    }), 201);
    const deniedDelete = await request.delete(`${endpoint(task)}/${owned.link.id}`, { headers: auth(foreignToken.token, owned.task.version) });
    expect(deniedDelete.status()).toBe(409);
    expect(await links(request, task)).toHaveLength(1);
    expect((await currentTask(request, task)).version).toBe(owned.task.version);
    await json(await request.delete(`${endpoint(task)}/${owned.link.id}`, { headers: auth(ownerToken.token, owned.task.version) }));
    expect(await links(request, task)).toEqual([]);
    await receipt(info, 'claim-enforcement-statuses', { foreignLink: deniedPost.status(), foreignUnlink: deniedDelete.status(), ownerLink: 201, ownerUnlink: 200 });
  });
});
