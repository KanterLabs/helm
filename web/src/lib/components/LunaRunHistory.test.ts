// @vitest-environment jsdom
import { mount, tick, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import LunaRunHistory from './LunaRunHistory.svelte';

const mountedComponents: Array<ReturnType<typeof mount>> = [];

afterEach(async () => {
  while (mountedComponents.length) await unmount(mountedComponents.pop()!);
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

describe('LunaRunHistory', () => {
  it('shows private run metadata and expands diagnostics', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: [{
        id: 'run-1',
        project_key: 'HELM',
        feature: 'task_draft',
        outcome: 'invalid_output',
        model: 'gpt-5.6-luna',
        effort: 'medium',
        thread_id: 'thread-1',
        turn_id: 'turn-1',
        duration_ms: 1234,
        output_bytes: 512,
        detail: 'unsupported task key',
        started_at: '2026-09-22T00:00:00Z',
        completed_at: '2026-09-22T00:00:01Z'
      }],
      next_cursor: ''
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })));

    const mounted = mount(LunaRunHistory, { target: document.body });
    mountedComponents.push(mounted);
    await vi.waitFor(() => expect(document.body.textContent).toContain('Invalid output'));

    expect(document.body.textContent).toContain('Task draft');
    expect(document.body.textContent).toContain('HELM');
    expect(document.body.textContent).not.toContain('unsupported task key');
    document.querySelector<HTMLButtonElement>('.luna-run-summary')?.click();
    await tick();
    expect(document.body.textContent).toContain('unsupported task key');
    expect(document.body.textContent).toContain('thread-1');
    expect(fetch).toHaveBeenCalledWith('/api/v1/codex/runs?limit=50', expect.objectContaining({ cache: 'no-store' }));
  });
});
