import { afterEach, describe, expect, it, vi } from 'vitest';
import { request } from './api';
import { offlineReadOnly } from './connectivity';
import { resetHostMigrationState, setHostMigrationMetadata } from './hostMigration';

afterEach(() => {
  offlineReadOnly.set(false);
  resetHostMigrationState();
  vi.restoreAllMocks();
});

describe('offline API boundary', () => {
  it.each(['POST', 'PUT', 'PATCH', 'DELETE'])('blocks %s before fetch without queuing it', async method => {
    const fetch = vi.spyOn(globalThis, 'fetch');
    offlineReadOnly.set(true);
    await expect(request('/tasks/one', { method, body: { title: 'Do not queue' } })).rejects.toMatchObject({ code: 'offline_read_only' });
    expect(fetch).not.toHaveBeenCalled();
    offlineReadOnly.set(false);
    await Promise.resolve();
    expect(fetch).not.toHaveBeenCalled();
  });

  it('blocks old-origin writes before fetch and never replays them to the new host', async () => {
    const fetch = vi.spyOn(globalThis, 'fetch');
    setHostMigrationMetadata({ canonical_origin: 'https://new.example', legacy_origin: 'https://old.example' }, 'https://old.example');

    await expect(request('/tasks/one', { method: 'PATCH', body: { title: 'Do not replay' } })).rejects.toMatchObject({
      status: 409,
      code: 'instance_moved'
    });
    expect(fetch).not.toHaveBeenCalled();
  });

  it('allows the explicit old-origin logout escape hatch', async () => {
    const fetch = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}'));
    setHostMigrationMetadata({ canonical_origin: 'https://new.example', legacy_origin: 'https://old.example' }, 'https://old.example');

    await expect(request('/auth/logout', { method: 'POST' })).resolves.toEqual({});
    expect(fetch).toHaveBeenCalledOnce();
  });

  it('blocks writes if the browser goes offline before the UI reacts', async () => {
    vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false);
    const fetch = vi.spyOn(globalThis, 'fetch');
    await expect(request('/tasks', { method: 'post' })).rejects.toMatchObject({ code: 'offline_read_only' });
    expect(fetch).not.toHaveBeenCalled();
  });

  it('allows uncached auth reads while waiting to re-enable writes', async () => {
    offlineReadOnly.set(true);
    const fetch = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}'));
    await request('/auth/status');
    expect(fetch.mock.calls[0][1]).toMatchObject({ cache: 'no-store', credentials: 'include' });
  });

  it('signals unreachable servers without retrying a potentially committed write', async () => {
    const fetch = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'));
    const events = vi.spyOn(window, 'dispatchEvent');
    await expect(request('/tasks/one', { method: 'PATCH' })).rejects.toThrow('Failed to fetch');
    expect(events.mock.calls.some(([event]) => event.type === 'helm:network-unavailable')).toBe(true);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('surfaces an instance move response without retrying the mutation', async () => {
    const fetch = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      error: {
        code: 'instance_moved',
        message: 'This instance moved.',
        details: { canonical_origin: 'https://new.example' }
      }
    }), { status: 409, headers: { 'Content-Type': 'application/json' } }));
    const events = vi.spyOn(window, 'dispatchEvent');

    await expect(request('/tasks/one', { method: 'PATCH', body: { title: 'Do not replay' } })).rejects.toMatchObject({
      status: 409,
      code: 'instance_moved',
      details: { canonical_origin: 'https://new.example' }
    });
    expect(fetch).toHaveBeenCalledOnce();
    expect(events.mock.calls.some(([event]) => event.type === 'helm:auth-invalidated')).toBe(false);
  });

  it('leaves auth network redirects to the bootstrap flow', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'));
    const events = vi.spyOn(window, 'dispatchEvent');
    await expect(request('/auth/status')).rejects.toThrow();
    expect(events.mock.calls.some(([event]) => event.type === 'helm:network-unavailable')).toBe(false);
  });

  it.each([401, 403])('invalidates saved authorization after HTTP %s', async status => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status }));
    const events = vi.spyOn(window, 'dispatchEvent');
    await expect(request('/projects')).rejects.toMatchObject({ status });
    expect(events.mock.calls.some(([event]) => event.type === 'helm:auth-invalidated')).toBe(true);
  });

  it('still clears Helm boards on auth revocation during a move', async () => {
    setHostMigrationMetadata({ canonical_origin: 'https://new.example', legacy_origin: 'https://old.example' }, 'https://old.example');
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status: 401 }));
    const events = vi.spyOn(window, 'dispatchEvent');

    await expect(request('/projects')).rejects.toMatchObject({ status: 401 });
    expect(events.mock.calls.some(([event]) => event.type === 'helm:auth-invalidated')).toBe(true);
  });
});
