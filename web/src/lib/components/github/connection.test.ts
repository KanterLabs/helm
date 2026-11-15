import { describe, expect, it } from 'vitest';
import { ApiError } from '../../types';
import { badge, healthNote, plainError } from './connection';

// Failure list: ./failures.md, entry 21.
describe('a connected integration that has not been checked yet', () => {
  const never = '0001-01-01T00:00:00Z';
  const checked = '2026-11-15T12:00:00Z';

  it('is not reported as unreachable', () => {
    expect(healthNote('unavailable', never)).toBe('');
    expect(healthNote('unavailable', undefined)).toBe('');
    expect(badge('connected', 'unavailable', never)).toEqual({ label: 'Connected', state: 'connected' });
  });

  it('still reports a check that failed', () => {
    expect(healthNote('unavailable', checked)).toContain('could not reach GitHub');
    expect(badge('connected', 'unavailable', checked)).toEqual({ label: 'Unavailable', state: 'unavailable' });
    expect(healthNote('permission_required', never)).toContain('cannot read');
    expect(badge('connected', 'rate_limited', never).label).toBe('Rate limited');
  });
});

// Failure list: ./failures.md, entry 23.
describe('a sign-in that ended while testing the connection', () => {
  it('says to reconnect, not to try again', () => {
    const ended = new ApiError('GitHub sign-in has ended; reconnect GitHub.', 409, 'github_reconnect_required');
    expect(plainError(ended, 'fallback')).toBe('GitHub sign-in has ended. Press Reconnect GitHub to connect again.');
  });
});
