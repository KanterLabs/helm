import { test, expect } from '@playwright/test';
import { randomBytes } from 'node:crypto';
import { DatabaseSync } from 'node:sqlite';

test('legacy agent usage no longer blocks writes, while burst admission remains', async ({ request }, testInfo) => {
  const dbPath = process.env.HELM_E2E_DB;
  test.skip(!dbPath, 'requires the real-process E2E database');

  const unique = `B${randomBytes(6).toString('hex').toUpperCase()}`;
  const origin = new URL(process.env.HELM_E2E_BASE_URL!).origin;
  const createAgent = await request.post('/api/v1/agents', {
    data: { name: `Legacy usage ${unique}` },
    headers: { Origin: origin, 'Idempotency-Key': `${unique}-agent` }
  });
  expect(createAgent.status()).toBe(201);
  const agent = await createAgent.json() as { id: string };

  const createToken = await request.post(`/api/v1/agents/${agent.id}/tokens`, {
    data: { name: 'legacy-usage-e2e', scopes: ['projects:write', 'projects:read'] },
    headers: { Origin: origin }
  });
  expect(createToken.status()).toBe(201);
  const issued = await createToken.json() as { token: string };
  const createSecondToken = await request.post(`/api/v1/agents/${agent.id}/tokens`, {
    data: { name: 'legacy-usage-e2e-second', scopes: ['projects:write'] },
    headers: { Origin: origin }
  });
  expect(createSecondToken.status()).toBe(201);
  const secondToken = await createSecondToken.json() as { token: string };

  const db = new DatabaseSync(dbPath!);
  const legacyBytes = 256 * 1024 * 1024;
  try {
    db.prepare('INSERT INTO actor_resource_usage(actor_id, reserved_bytes, updated_at) VALUES (?, ?, ?)')
      .run(agent.id, legacyBytes, new Date().toISOString());

    // The mutation bucket refills once per second, so ten completed sequential
    // writes do not necessarily exhaust it. Run two streams of ten instead:
    // each token is within its own burst allowance, and a mutation rejection
    // therefore proves the allowance is shared by the actor. Two in-flight
    // requests also stay below the server's four bearer-authentication slots.
    const started = Date.now();
    const streams = await Promise.all([issued.token, secondToken.token].map(async (token, tokenIndex) => {
      const results = [];
      for (let index = 0; index < 10; index++) {
        const key = `${unique}${tokenIndex}${index}`;
        const response = await request.post('/api/v1/projects', {
          data: { key, name: `Legacy usage project ${key}` },
          headers: { Authorization: `Bearer ${token}`, 'Idempotency-Key': `${key}-project` }
        });
        const body = await response.json();
        results.push({
          tokenIndex, index, key, status: response.status(), elapsedMs: Date.now() - started,
          retryAfter: response.headers()['retry-after'], error: body.error ?? null
        });
      }
      return results;
    }));
    const results = streams.flat();
    await testInfo.attach('agent-mutation-admission.json', {
      contentType: 'application/json',
      body: JSON.stringify({ legacyBytes, elapsedMs: Date.now() - started, results }, null, 2)
    });

    // Both credentials can write despite the legacy usage row. The first ten
    // admitted mutations succeed; refill may permit additional successes.
    for (const stream of streams) expect(stream[0].status).toBe(201);
    for (const result of results) {
      expect([201, 429], JSON.stringify(result)).toContain(result.status);
      if (result.status === 429) {
        expect(result.error).toMatchObject({ code: 'rate_limited', message: 'too many agent mutations; retry later' });
        expect(Number(result.retryAfter)).toBeGreaterThan(0);
      }
      const project = db.prepare('SELECT key FROM projects WHERE key = ?').get(result.key);
      expect(Boolean(project), JSON.stringify(result)).toBe(result.status === 201);
    }
    expect(results.filter((result) => result.status === 201).length).toBeGreaterThanOrEqual(10);
    expect(results.some((result) => result.status === 429), JSON.stringify(results)).toBe(true);

    const row = db.prepare('SELECT reserved_bytes FROM actor_resource_usage WHERE actor_id = ?')
      .get(agent.id) as { reserved_bytes: number };
    expect(row.reserved_bytes).toBe(legacyBytes);
  } finally {
    db.close();
  }
});
