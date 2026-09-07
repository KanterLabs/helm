import { clearOfflineBoards } from './offlineBoards';

/** Origins supplied by the runtime while an instance is being moved. */
export interface HostMigrationMetadata {
  canonical_origin?: unknown;
  legacy_origin?: unknown;
}

export interface HostMigrationState {
  /** True only when both origins are valid, distinct HTTPS origins. */
  enabled: boolean;
  /** The normalized destination origin. Empty when metadata is invalid/absent. */
  canonicalOrigin: string;
  /** The normalized old origin. Empty when metadata is invalid/absent. */
  legacyOrigin: string;
  /** The normalized origin of this page, when available. */
  currentOrigin: string;
  /** Whether this page is on the old origin and must stay read-only. */
  active: boolean;
}

export type HostMigrationListener = (state: HostMigrationState) => void;

const EMPTY_STATE: HostMigrationState = {
  enabled: false,
  canonicalOrigin: '',
  legacyOrigin: '',
  currentOrigin: '',
  active: false
};

let state: HostMigrationState = { ...EMPTY_STATE };
const listeners = new Set<HostMigrationListener>();

/**
 * Normalize a runtime origin without accepting URLs that can smuggle a path,
 * credentials, query, fragment, or non-HTTPS destination into navigation.
 */
export function normalizeHttpsOrigin(value: unknown): string | null {
  if (typeof value !== 'string') return null;
  const source = value.trim();
  if (!source || source.length > 2048) return null;
  // WHATWG URL parsing intentionally repairs backslashes and empty query /
  // fragment markers. Runtime metadata is an exact-origin contract, so reject
  // those spellings before parsing instead of accepting a repaired URL.
  if (
    !/^https:\/\//i.test(source)
    || /\s/.test(source)
    || /[\\\u0000-\u001f\u007f]/.test(source)
    || source.includes('?')
    || source.includes('#')
  ) return null;
  try {
    const parsed = new URL(source);
    if (parsed.protocol !== 'https:') return null;
    if (!parsed.hostname || parsed.username || parsed.password) return null;
    if (parsed.pathname !== '/' || parsed.search || parsed.hash) return null;
    return parsed.origin;
  } catch {
    return null;
  }
}

function normalizeCurrentOrigin(value?: unknown): string {
  const candidate = typeof value === 'string'
    ? value
    : typeof window !== 'undefined'
      ? window.location.origin
      : '';
  if (!candidate) return '';
  try {
    return new URL(candidate).origin;
  } catch {
    return '';
  }
}

/**
 * Derive migration state from untrusted response metadata. Invalid or partial
 * metadata deliberately behaves like an old server with no migration.
 */
export function migrationStateFromMetadata(
  metadata: HostMigrationMetadata | null | undefined,
  currentOrigin?: string
): HostMigrationState {
  const canonicalOrigin = normalizeHttpsOrigin(metadata?.canonical_origin);
  const legacyOrigin = normalizeHttpsOrigin(metadata?.legacy_origin);
  const current = normalizeCurrentOrigin(currentOrigin);
  const enabled = Boolean(canonicalOrigin && legacyOrigin && canonicalOrigin !== legacyOrigin);
  return {
    enabled,
    canonicalOrigin: enabled ? canonicalOrigin || '' : '',
    legacyOrigin: enabled ? legacyOrigin || '' : '',
    currentOrigin: current,
    active: enabled && current === legacyOrigin
  };
}

/** Publish fresh metadata to the request guard and interested UI surfaces. */
export function setHostMigrationMetadata(
  metadata: HostMigrationMetadata | null | undefined,
  currentOrigin?: string
): HostMigrationState {
  state = migrationStateFromMetadata(metadata, currentOrigin);
  listeners.forEach((listener) => listener(state));
  return state;
}

export function getHostMigrationState(): HostMigrationState {
  return state;
}

export function subscribeHostMigration(listener: HostMigrationListener): () => void {
  listeners.add(listener);
  listener(state);
  return () => listeners.delete(listener);
}

/** A test-safe reset that also restores the default absent-metadata behavior. */
export function resetHostMigrationState(): void {
  state = { ...EMPTY_STATE };
  listeners.forEach((listener) => listener(state));
}

/**
 * Preserve only Helm project routes. Search, filter, and other query strings
 * may contain task text or tokens, so they are intentionally discarded.
 */
export function safeProjectPath(pathname: unknown): string {
  if (typeof pathname !== 'string') return '/';
  if (
    pathname.includes('\\')
    || pathname.includes('?')
    || pathname.includes('#')
    || /[\u0000-\u001f\u007f]/.test(pathname)
    || /%(?:2f|5c|2e|00|3f|23)/i.test(pathname)
    || pathname.split('/').some((segment) => segment === '.' || segment === '..')
  ) return '/';
  const match = pathname.match(
    /^\/p\/[^/]+(?:\/(?:roadmap|timeline|audits(?:\/[^/]+)?|tasks\/[^/]+))?\/?$/
  );
  return match ? match[0] : '/';
}

/**
 * Build the only cross-origin navigation Helm may initiate for a move. The
 * destination comes from validated metadata and the path from a strict route
 * allowlist; query strings and fragments are always removed.
 */
export function safeMigrationUrl(
  canonicalOrigin: unknown,
  currentLocation?: string | { pathname?: string }
): string | null {
  const canonical = normalizeHttpsOrigin(canonicalOrigin);
  if (!canonical) return null;
  let pathname = '/';
  if (typeof currentLocation === 'string') {
    try {
      pathname = new URL(currentLocation, 'https://invalid.local').pathname;
    } catch {
      pathname = '/';
    }
  } else if (currentLocation && typeof currentLocation.pathname === 'string') {
    pathname = currentLocation.pathname;
  } else if (typeof window !== 'undefined') {
    pathname = window.location.pathname;
  }
  const target = new URL(canonical);
  target.pathname = safeProjectPath(pathname);
  target.search = '';
  target.hash = '';
  return target.href;
}

/**
 * Delete only Helm's own offline board database and generated static caches.
 * We intentionally do not clear localStorage/sessionStorage or arbitrary site
 * data: browser sessions, drafts, and other applications are not transferable.
 */
export interface HelmMigrationCleanupResult {
  offlineBoardsCleared: boolean;
  staticCachesCleared: boolean;
}

export async function clearHelmMigrationStorage(): Promise<HelmMigrationCleanupResult> {
  const offlineBoardsCleared = await clearOfflineBoards();
  if (typeof caches === 'undefined') return { offlineBoardsCleared, staticCachesCleared: true };
  try {
    const names = await caches.keys();
    const results = await Promise.all(
      names
        .filter((name) => name.startsWith('helm-static-v'))
        .map((name) => caches.delete(name))
    );
    return { offlineBoardsCleared, staticCachesCleared: results.every(Boolean) };
  } catch {
    // CacheStorage may be unavailable or denied in private browsing. The
    // IndexedDB clear above is still attempted and remains independently safe.
    return { offlineBoardsCleared, staticCachesCleared: false };
  }
}
