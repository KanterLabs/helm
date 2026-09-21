import { describe, expect, it } from 'vitest';
import { offlineShellCacheName } from '../vite.config';

describe('offline shell build identity', () => {
  it('is content-addressed so an identical rebuild does not advertise an update', () => {
    expect(offlineShellCacheName('abc123')).toBe('helm-static-vabc123');
    expect(offlineShellCacheName('abc123')).toBe(offlineShellCacheName('abc123'));
  });

  it('changes when the offline shell content changes', () => {
    expect(offlineShellCacheName('abc123')).not.toBe(offlineShellCacheName('def456'));
  });
});
