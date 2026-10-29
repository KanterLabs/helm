import { expect, test, type APIRequestContext, type TestInfo } from '@playwright/test';
import { api, repository, seed, sentinel, sourceSHA, waitJob, waitJobStage, type BetaTask } from './support/githubBetaFixture';

// Tokens are real, short-lived credentials. Only sanitized JSON receipts are
// retained: Playwright traces would include token issue responses and headers.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

type Token = { id: string; token: string };
type Grant = { id: string; project_id: string; agent_id: string; token_id: string; account_owner_id: string; max_tasks: number };
type Job = { requested_by: string; account_owner_id: string; authorization_id: string; outcome: string; stage: string };
type Finding = { id: string; task_id: string; assessment: import('../src/lib/types').GithubAssessment };
const origin = process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18082';
const expires = (ms = 60 * 60_000) => new Date(Date.now() + ms).toISOString();
const grantsPath = (project: string) => `/projects/${project}/github-audit-authorizations`;
const auditPath = (project: string) => `/projects/${project}/github-audits`;
const readinessPath = (project: string, grant = '') => `/projects/${project}/github/readiness${grant ? `?authorization_id=${grant}` : ''}`;

async function agentRequest(request: APIRequestContext, token: Token, method: string, path: string, body?: unknown, key?: string) {
  const response = await request.fetch(`/api/v1${path}`, {
    method, data: body,
    headers: { Authorization: `Bearer ${token.token}`, ...(key ? { 'Idempotency-Key': key } : {}) },
  });
  const text = await response.text();
  expect(text.includes(sentinel) || text.includes(token.token), 'API response must not expose credentials').toBeFalsy();
  return { status: response.status(), body: text ? JSON.parse(text) : null };
}

async function agentJSON(request: APIRequestContext, token: Token, method: string, path: string, body?: unknown, key?: string) {
  const result = await agentRequest(request, token, method, path, body, key);
  expect(result.status, `${method} ${path}`).toBeGreaterThanOrEqual(200);
  expect(result.status, `${method} ${path}`).toBeLessThan(300);
  return result.body;
}

async function issue(request: APIRequestContext, agentID: string, projects: string[]): Promise<Token> {
  // Do not attach this response or include its body in assertion messages.
  const response = await request.post(`/api/v1/agents/${agentID}/tokens`, {
    data: { name: 'bounded audit e2e', scopes: ['tasks:read'], project_ids: projects, expires_at: expires() },
    headers: { Origin: origin },
  });
  expect(response.status()).toBe(201);
  const { id, token } = await response.json();
  return { id, token };
}

async function actor(request: APIRequestContext, projectIDs: string[]) {
  const agent = await api(request, 'POST', '/agents', { name: `Audit reader ${Date.now()}`, project_ids: projectIDs });
  return { id: agent.id as string, token: await issue(request, agent.id, projectIDs) };
}

async function configure(request: APIRequestContext, project: string) {
  const selection = await api(request, 'GET', `/projects/${project}/github`);
  await api(request, 'PUT', `/projects/${project}/github`, { repositories: [{ repository, ref: 'main' }] }, selection.version);
}

async function grant(request: APIRequestContext, project: string, agent: string, token: Token, maxTasks = 1, expiry = expires()): Promise<Grant> {
  return api(request, 'POST', grantsPath(project), { agent_id: agent, token_id: token.id, expires_at: expiry, max_tasks: maxTasks });
}

async function state(request: APIRequestContext) {
  const response = await request.get('/__githubbeta/state');
  expect(response.status()).toBe(200);
  return response.json();
}

async function job(request: APIRequestContext, audit: string): Promise<Job> {
  return api(request, 'GET', `/audits/${audit}/job`);
}

async function assertUnchanged(request: APIRequestContext, tasks: BetaTask[]) {
  for (const task of tasks) {
    const current = await api(request, 'GET', `/tasks/${task.id}`);
    expect({ version: current.version, column_id: current.column_id }).toEqual({ version: task.version, column_id: task.column_id });
  }
}

async function receipt(info: TestInfo, name: string, safe: unknown, tokens: Token[]) {
  const body = JSON.stringify(safe, null, 2);
  expect(body.includes(sentinel)).toBeFalsy();
  for (const token of tokens) expect(body.includes(token.token)).toBeFalsy();
  await info.attach(name, { body, contentType: 'application/json' });
}

test.describe('GitHub delegated audit real backend', () => {
  test.skip(process.env.HELM_GITHUB_BETA_E2E !== '1', 'Dedicated synthetic upstream harness required');
  test.setTimeout(120_000);

  test('binds the real agent and human AI owner, replays once, and preserves grants across restart', async ({ request }, info) => {
    const { project, tasks } = await seed(request, `C${Date.now().toString().slice(-7)}`);
    await configure(request, project.id);
    const human = (await api(request, 'GET', '/auth/status')).actor;
    const agent = await actor(request, [project.id]);
    const outsider = await actor(request, [project.id]);
    const authorization = await grant(request, project.id, agent.id, agent.token);
    expect(authorization).toMatchObject({ project_id: project.id, agent_id: agent.id, token_id: agent.token.id, account_owner_id: human.id, max_tasks: 1 });
    expect((await api(request, 'GET', grantsPath(project.id))).data).toEqual(expect.arrayContaining([expect.objectContaining({ id: authorization.id })]));
    expect((await agentJSON(request, agent.token, 'GET', grantsPath(project.id))).data).toEqual([expect.objectContaining({ id: authorization.id })]);
    expect((await agentJSON(request, outsider.token, 'GET', grantsPath(project.id))).data).toEqual([]);
    expect((await agentJSON(request, agent.token, 'GET', readinessPath(project.id, authorization.id))).ready).toBe(true);

    const before = await state(request);
    const scope = { authorization_id: authorization.id, task_ids: [tasks[0].id] };
    const key = `agent-audit-${project.id}`;
    const started = await agentJSON(request, agent.token, 'POST', auditPath(project.id), scope, key);
    const replay = await agentJSON(request, agent.token, 'POST', auditPath(project.id), scope, key);
    expect(replay.audit.id).toBe(started.audit.id);
    await waitJob(request, started.audit.id);
    const completed = await agentJSON(request, agent.token, 'GET', `/audits/${started.audit.id}/job`);
    expect(completed).toMatchObject({ requested_by: agent.id, account_owner_id: human.id, authorization_id: authorization.id });
    const upstream = await state(request);
    expect(upstream.ai_calls - before.ai_calls).toBe(1);
    expect(upstream.last_ai_actor).toBe(human.id);
    const evidence = await agentJSON(request, agent.token, 'GET', `/audits/${started.audit.id}/evidence`);
    expect(evidence.repositories).toEqual([expect.objectContaining({ repository, sha: sourceSHA })]);
    expect(evidence.tasks.map((task: { id: string }) => task.id)).toEqual([tasks[0].id]);
    const findings: Finding[] = (await agentJSON(request, agent.token, 'GET', `/audits/${started.audit.id}/findings`)).data;
    expect(findings).toHaveLength(1);
    expect(findings[0].task_id).toBe(tasks[0].id);
    const assessment = findings[0].assessment;
    expect(assessment).toMatchObject({ assessment_version: 2, placement: 'correction_recommended', target_semantic_state: 'ready', lifecycle_action: 'move' });
    const citations = [...assessment.conditions, ...assessment.basis].flatMap(item => item.citation_ids);
    expect(citations.length).toBeGreaterThan(0);
    expect(citations.every(ref => evidence.items.some((item: { id: string }) => item.id === ref))).toBe(true);
    expect(assessment.basis.some(basis => basis.citation_ids.some(ref => evidence.items.some((item: { id: string; source_role: string; evidence_class: string }) =>
      item.id === ref && item.source_role === 'task_context' && item.evidence_class === 'task_fact')))).toBe(true);
    const applyDenied = await agentRequest(request, agent.token, 'POST', `/audits/${started.audit.id}/apply`, {
      assessment_version: 2, preview: false, items: [{ finding_id: findings[0].id }],
    }, `agent-apply-denied-${project.id}`);
    expect(applyDenied.status).toBe(403);
    await assertUnchanged(request, tasks);

    const restarted = await request.post('/__githubbeta/restart');
    expect(restarted.status()).toBe(200);
    expect(await agentJSON(request, agent.token, 'GET', `/audits/${started.audit.id}/job`)).toMatchObject(completed);
    expect((await agentJSON(request, agent.token, 'GET', grantsPath(project.id))).data).toEqual([expect.objectContaining({ id: authorization.id })]);
    expect((await agentJSON(request, agent.token, 'POST', auditPath(project.id), scope, key)).audit.id).toBe(started.audit.id);
    expect((await state(request)).ai_calls).toBe(upstream.ai_calls);
    await receipt(info, 'delegated-audit-attribution.json', {
      project: project.id, authorization, job: completed, inference: { calls: 1, account_owner: upstream.last_ai_actor },
      evidence, findings, apply_denied: applyDenied.status,
      tasks_unchanged: true, restart_persisted: true,
    }, [agent.token, outsider.token]);
  });

  test('denies scope expansion and fences expiry, grant revocation and token deletion before publication', async ({ request }, info) => {
    const { project, tasks } = await seed(request, `D${Date.now().toString().slice(-7)}`);
    const { project: other, tasks: otherTasks } = await seed(request, `E${Date.now().toString().slice(-7)}`);
    await configure(request, project.id);
    await configure(request, other.id);
    const agent = await actor(request, [project.id, other.id]);
    const wrongToken = await issue(request, agent.id, [project.id, other.id]);
    const authorization = await grant(request, project.id, agent.id, agent.token);
    const scope = { authorization_id: authorization.id, task_ids: [tasks[0].id] };
    const denied: Record<string, number> = {};
    const reject = async (name: string, token: Token, method: string, path: string, body?: unknown, allowed = [403]) => {
      denied[name] = (await agentRequest(request, token, method, path, body, `deny-${name}-${project.id}`)).status;
      expect(allowed, name).toContain(denied[name]);
    };
    await reject('readiness_without_grant', agent.token, 'GET', readinessPath(project.id));
    await reject('start_without_grant', agent.token, 'POST', auditPath(project.id), { task_ids: [tasks[0].id] });
    await reject('wrong_token_readiness', wrongToken, 'GET', readinessPath(project.id, authorization.id));
    await reject('wrong_token_start', wrongToken, 'POST', auditPath(project.id), scope);
    await reject('wrong_project', agent.token, 'POST', auditPath(other.id), { authorization_id: authorization.id, task_ids: [otherTasks[0].id] });
    await reject('bearer_create', agent.token, 'POST', grantsPath(project.id), { agent_id: agent.id, token_id: agent.token.id, expires_at: expires() });
    await reject('bearer_revoke', agent.token, 'DELETE', `${grantsPath(project.id)}/${authorization.id}`);
    await reject('unbounded_tasks', agent.token, 'POST', auditPath(project.id), { authorization_id: authorization.id }, [400, 403]);
    await reject('state_scope', agent.token, 'POST', auditPath(project.id), { ...scope, states: ['backlog'] }, [400, 403]);
    await reject('max_tasks', agent.token, 'POST', auditPath(project.id), { ...scope, task_ids: tasks.map(task => task.id) }, [400, 403]);

    for (const [name, body] of [
      ['over_24h', { expires_at: expires(25 * 60 * 60_000), max_tasks: 1 }],
      ['over_100_tasks', { expires_at: expires(), max_tasks: 101 }],
    ] as const) {
      const response = await request.post(`/api/v1${grantsPath(project.id)}`, {
        headers: { Origin: origin }, data: { agent_id: agent.id, token_id: agent.token.id, ...body },
      });
      denied[name] = response.status();
      expect(response.status(), name).toBe(400);
    }
    const expired = await grant(request, project.id, agent.id, wrongToken, 1, expires(2000));
    await expect.poll(() => Date.now() > Date.parse((expired as Grant & { expires_at: string }).expires_at), { timeout: 5000 }).toBe(true);
    await reject('expired_grant', wrongToken, 'POST', auditPath(project.id), { authorization_id: expired.id, task_ids: [tasks[0].id] });

    const fenced: { reason: string; job: Job; findings: number }[] = [];
    await request.put('/__githubbeta/state', { data: { delay_ms: 3000 } });
    try {
      for (const reason of ['grant_revoked', 'token_deleted']) {
        const token = reason === 'grant_revoked' ? agent.token : await issue(request, agent.id, [project.id]);
        const activeGrant = reason === 'grant_revoked' ? authorization : await grant(request, project.id, agent.id, token);
        const started = await agentJSON(request, token, 'POST', auditPath(project.id), { authorization_id: activeGrant.id, task_ids: [tasks[0].id] }, `${reason}-${project.id}`);
        await waitJobStage(request, started.audit.id, 'analyzing');
        if (reason === 'grant_revoked') {
          const revoked = await request.delete(`/api/v1${grantsPath(project.id)}/${activeGrant.id}`, { headers: { Origin: origin } });
          expect([200, 204]).toContain(revoked.status());
          await reject('revoked_readiness', token, 'GET', readinessPath(project.id, activeGrant.id));
          denied.revoked_replay = (await agentRequest(request, token, 'POST', auditPath(project.id), {
            authorization_id: activeGrant.id, task_ids: [tasks[0].id],
          }, `${reason}-${project.id}`)).status;
          expect(denied.revoked_replay).toBe(403);
        } else {
          const deleted = await request.delete(`/api/v1/tokens/${token.id}`, { headers: { Origin: origin } });
          expect(deleted.status()).toBe(204);
        }
        // Wait past the inference delay as well as the terminal state: an early
        // cancel receipt alone cannot prove that late findings stay unpublished.
        const afterInference = Date.now() + 3400;
        await expect.poll(async () => {
          const current = await job(request, started.audit.id);
          return Boolean(current.outcome) && Date.now() >= afterInference;
        }, { timeout: 20_000 }).toBe(true);
        const current = await job(request, started.audit.id);
        expect(current.outcome).not.toBe('complete');
        const findings = (await api(request, 'GET', `/audits/${started.audit.id}/findings`)).data;
        expect(findings).toHaveLength(0);
        fenced.push({ reason, job: current, findings: findings.length });
      }
    } finally {
      await request.put('/__githubbeta/state', { data: { delay_ms: 0 } });
    }
    await assertUnchanged(request, [...tasks, ...otherTasks]);
    await receipt(info, 'delegated-audit-denials-and-fencing.json', { project: project.id, denied, fenced, tasks_unchanged: true }, [agent.token, wrongToken]);
  });
});
