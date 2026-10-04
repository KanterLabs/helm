import { describe, expect, it } from 'vitest';
import { mapWithConcurrency } from './concurrency';

describe('mapWithConcurrency', () => {
  it('keeps order and never exceeds the limit', async () => {
    let active = 0;
    let peak = 0;
    const result = await mapWithConcurrency(Array.from({ length: 25 }, (_, index) => index), 4, async (value) => {
      active += 1;
      peak = Math.max(peak, active);
      await new Promise((resolve) => setTimeout(resolve, value % 3));
      active -= 1;
      return value * 2;
    });
    expect(result).toEqual(Array.from({ length: 25 }, (_, index) => index * 2));
    expect(peak).toBe(4);
  });

  it('rejects like Promise.all and handles an empty list', async () => {
    await expect(mapWithConcurrency([1, 2, 3], 2, async (value) => {
      if (value === 2) throw new Error('boom');
      return value;
    })).rejects.toThrow('boom');
    expect(await mapWithConcurrency([], 3, async (value) => value)).toEqual([]);
  });
});
