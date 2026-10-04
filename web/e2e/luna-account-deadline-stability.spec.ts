import { expect, test, type APIRequestContext, type APIResponse, type TestInfo } from '@playwright/test';

type Project = { id: string; key: string };
type ErrorResponse = {
  error?: {
    code?: string;
    message?: string;
    details?: Record<string, unknown>;
  };
};

const e2eOrigin = new URL(
  process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080'
).origin;

async function assertTimedOut(
  response: APIResponse,
  expectedCode: string,
  attachmentName: string,
  testInfo: TestInfo,
  startedAt: number
): Promise<ErrorResponse> {
  const body = await response.text();
  await testInfo.attach(attachmentName, {
    body: Buffer.from(body),
    contentType: 'application/json'
  });
  expect(Date.now() - startedAt, `${attachmentName} should honor the account deadline`).toBeLessThan(25_000);
  expect(response.status()).toBe(503);
  expect(response.headers()['content-type']).toContain('application/json');
  const payload = JSON.parse(body) as ErrorResponse;
  expect(payload).toMatchObject({
    error: {
      code: expectedCode,
      details: {}
    }
  });
  expect(body).not.toMatch(/prompt|token|credential|account_id|actor_id/i);
  return payload;
}

async function assertReady(request: APIRequestContext, testInfo: TestInfo, name: string) {
  const response = await request.get('/readyz', { timeout: 30_000 });
  const body = await response.text();
  await testInfo.attach(name, {
    body: Buffer.from(body),
    contentType: 'application/json'
  });
  expect(response.status()).toBe(200);
  expect(body).toContain('"status":"ok"');
}

test('returns a bounded task-draft error when Codex account/read stalls', async ({ request }, testInfo) => {
  test.setTimeout(40_000);
  test.skip(
    process.env.HELM_E2E_CODEX_STALL_ACCOUNT !== 'true',
    'requires HELM_E2E_CODEX_STALL_ACCOUNT=true so the fake Codex stalls account/read'
  );

  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const created = await request.post('/api/v1/projects', {
    data: { key: `DL${suffix}`.slice(0, 16), name: `Deadline E2E ${suffix}` },
    headers: {
      Origin: e2eOrigin,
      'Content-Type': 'application/json',
      'Idempotency-Key': `luna-account-deadline-${suffix}`
    },
    timeout: 30_000
  });
  expect(created.status()).toBe(201);
  const project = await created.json() as Project;

  const startedAt = Date.now();
  const response = await request.post(`/api/v1/projects/${encodeURIComponent(project.key)}/task-draft`, {
    data: { query: 'bounded account stall check' },
    headers: {
      Origin: e2eOrigin,
      'Content-Type': 'application/json'
    },
    timeout: 30_000
  });
  await assertTimedOut(response, 'luna_unavailable', 'luna-task-draft-timeout-response.json', testInfo, startedAt);
  await assertReady(request, testInfo, 'luna-task-draft-ready-response.json');
});

test('returns a bounded project-analysis error when Codex account/read stalls', async ({ request }, testInfo) => {
  test.setTimeout(40_000);
  test.skip(
    process.env.HELM_E2E_CODEX_STALL_ACCOUNT !== 'true',
    'requires HELM_E2E_CODEX_STALL_ACCOUNT=true so the fake Codex stalls account/read'
  );

  const startedAt = Date.now();
  const response = await request.post('/api/v1/project-intelligence/analyze', {
    headers: { Origin: e2eOrigin },
    timeout: 30_000
  });
  await assertTimedOut(response, 'luna_timed_out', 'luna-project-intelligence-timeout-response.json', testInfo, startedAt);
  await assertReady(request, testInfo, 'luna-project-intelligence-ready-response.json');
});
