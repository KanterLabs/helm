import { describe, expect, it } from 'vitest';
import { relativeTime } from './pullRequests';

// Failure list: ./failures.md, entry 19.
describe('relativeTime', () => {
  const now = Date.parse('2026-11-15T12:00:00Z');

  it('treats a missing time and the server zero time as never', () => {
    expect(relativeTime(undefined, now)).toBe('never');
    expect(relativeTime(null, now)).toBe('never');
    expect(relativeTime('', now)).toBe('never');
    expect(relativeTime('0001-01-01T00:00:00Z', now)).toBe('never');
    expect(relativeTime('1969-12-31T23:59:59Z', now)).toBe('never');
  });

  it('keeps real times', () => {
    expect(relativeTime('2026-11-15T11:59:40Z', now)).toBe('just now');
    expect(relativeTime('2026-11-15T11:30:00Z', now)).toBe('30 min ago');
    expect(relativeTime('2026-11-15T02:00:00Z', now)).toBe('10 h ago');
    expect(relativeTime('2026-11-12T12:00:00Z', now)).toBe('3 d ago');
    expect(relativeTime('not a time', now)).toBe('at an unknown time');
  });
});
