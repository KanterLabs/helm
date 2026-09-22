import { test, expect } from '@playwright/test';
import { DatabaseSync } from 'node:sqlite';

test('legacy agent usage no longer blocks writes, while burst admission remains', async ({ request }) => {
  const dbPath = process.env.HELM_E2E_DB;
  test.skip(!dbPath, 'requires the real-process E2E database');

  const unique = `B${Date.now().toString(36).toUpperCase()}`;
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

    for (let index = 0; index < 10; index++) {
      const response = await request.post('/api/v1/projects', {
        data: { key: `${unique}${index}`, name: `Legacy usage project ${index}` },
        headers: { Authorization: `Bearer ${issued.token}`, 'Idempotency-Key': `${unique}-${index}` }
      });
      expect(response.status(), await response.text()).toBe(201);
    }

    const limited = await request.post('/api/v1/projects', {
      data: { key: `${unique}LIMIT`, name: 'Must be rate limited' },
      headers: { Authorization: `Bearer ${secondToken.token}`, 'Idempotency-Key': `${unique}-limited` }
    });
    expect(limited.status()).toBe(429);
    expect(limited.headers()['retry-after']).toBeTruthy();

    const row = db.prepare('SELECT reserved_bytes FROM actor_resource_usage WHERE actor_id = ?')
      .get(agent.id) as { reserved_bytes: number };
    expect(row.reserved_bytes).toBe(legacyBytes);
  } finally {
    db.close();
  }
});
