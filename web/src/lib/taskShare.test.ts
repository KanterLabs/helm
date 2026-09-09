import { describe, expect, it, vi } from 'vitest';
import { buildTaskShareUrl, copyText } from './taskShare';

describe('task share helpers', () => {
  it('builds a stable absolute task URL without unrelated location state', () => {
    expect(buildTaskShareUrl('Product Ops', 'OPS-7', {
      origin: 'https://helm.example/p/Product%20Ops?token=secret#private',
      intent: 'details'
    })).toBe('https://helm.example/p/Product%20Ops/tasks/OPS-7');

    expect(buildTaskShareUrl('Product Ops', 'OPS-7', {
      origin: 'https://helm.example/search?q=secret',
      intent: 'activity'
    })).toBe('https://helm.example/p/Product%20Ops/tasks/OPS-7?view=activity');
  });

  it('falls back to a relative route when no safe origin is available', () => {
    expect(buildTaskShareUrl('Product Ops', 'OPS-7', { origin: '' }))
      .toBe('/p/Product%20Ops/tasks/OPS-7');
    expect(buildTaskShareUrl('', 'OPS-7', { origin: 'https://helm.example/?secret=1' })).toBe('');
  });

  it('reports successful writes only after the clipboard resolves', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    const result = await copyText('OPS-7', { writeText });
    expect(result).toEqual({ ok: true, value: 'OPS-7' });
    expect(writeText).toHaveBeenCalledWith('OPS-7');
  });

  it('returns an unavailable result when the clipboard API is missing', async () => {
    await expect(copyText('OPS-7', null)).resolves.toEqual({
      ok: false,
      value: 'OPS-7',
      reason: 'unavailable'
    });
  });

  it('returns a denied result when the clipboard rejects', async () => {
    const writeText = vi.fn().mockRejectedValue(new DOMException('Not allowed', 'NotAllowedError'));
    await expect(copyText('OPS-7', { writeText })).resolves.toEqual({
      ok: false,
      value: 'OPS-7',
      reason: 'denied'
    });
  });
});
