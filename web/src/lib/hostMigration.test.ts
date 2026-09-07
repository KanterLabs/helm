import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  clearHelmMigrationStorage,
  migrationStateFromMetadata,
  normalizeHttpsOrigin,
  resetHostMigrationState,
  safeMigrationUrl,
  safeProjectPath
} from './hostMigration';

afterEach(() => {
  resetHostMigrationState();
  vi.restoreAllMocks();
});

describe('hostname migration safeguards', () => {
  it('normalizes only HTTPS origins without URL smuggling', () => {
    expect(normalizeHttpsOrigin(' HTTPS://Old.Example:443/ ')).toBe('https://old.example');
    expect(normalizeHttpsOrigin('http://old.example')).toBeNull();
    expect(normalizeHttpsOrigin('https://old.example/path')).toBeNull();
    expect(normalizeHttpsOrigin('https://old.example/?next=https://attacker.example')).toBeNull();
    expect(normalizeHttpsOrigin('https://user:pass@old.example')).toBeNull();
    expect(normalizeHttpsOrigin('https:\\\\old.example')).toBeNull();
    expect(normalizeHttpsOrigin('https:evil.example')).toBeNull();
    expect(normalizeHttpsOrigin('https://old .example')).toBeNull();
  });

  it('activates only for an exact legacy-origin match with distinct metadata', () => {
    expect(migrationStateFromMetadata(undefined, 'https://old.example')).toMatchObject({ active: false, enabled: false });
    expect(migrationStateFromMetadata({ canonical_origin: 'https://new.example/path', legacy_origin: 'https://old.example' }, 'https://old.example')).toMatchObject({ active: false, enabled: false });
    expect(migrationStateFromMetadata({ canonical_origin: 'https://old.example', legacy_origin: 'https://old.example' }, 'https://old.example')).toMatchObject({ active: false, enabled: false });
    expect(migrationStateFromMetadata({ canonical_origin: 'https://new.example', legacy_origin: 'https://old.example' }, 'https://other.example')).toMatchObject({ active: false, enabled: true });
    expect(migrationStateFromMetadata({ canonical_origin: 'https://new.example', legacy_origin: 'https://old.example' }, 'https://old.example')).toMatchObject({
      active: true,
      enabled: true,
      canonicalOrigin: 'https://new.example',
      legacyOrigin: 'https://old.example'
    });
  });

  it('preserves only a safe project path and strips sensitive query data', () => {
    expect(safeMigrationUrl('https://new.example', 'https://old.example/p/ops/tasks/OPS-7?view=activity&token=secret#activity')).toBe('https://new.example/p/ops/tasks/OPS-7');
    expect(safeMigrationUrl('https://new.example', 'https://old.example/issues?q=secret')).toBe('https://new.example/');
    expect(safeProjectPath('/p/ops\\evil')).toBe('/');
    expect(safeProjectPath('/p/../admin')).toBe('/');
    expect(safeProjectPath('/p/ops%2Fadmin')).toBe('/');
    expect(safeProjectPath('/p/ops\u0000admin')).toBe('/');
    expect(safeProjectPath('/p/ops/roadmap')).toBe('/p/ops/roadmap');
  });

  it('clears only Helm static caches, not arbitrary site caches', async () => {
    const deleteCache = vi.fn(async () => true);
    Object.defineProperty(globalThis, 'caches', {
      configurable: true,
      value: {
        keys: vi.fn(async () => ['helm-static-v1', 'other-app-cache', 'helm-static-v2']),
        delete: deleteCache
      }
    });

    const result = await clearHelmMigrationStorage();

    expect(deleteCache).toHaveBeenCalledTimes(2);
    expect(deleteCache).toHaveBeenCalledWith('helm-static-v1');
    expect(deleteCache).toHaveBeenCalledWith('helm-static-v2');
    expect(deleteCache).not.toHaveBeenCalledWith('other-app-cache');
    expect(result.staticCachesCleared).toBe(true);
  });
});
