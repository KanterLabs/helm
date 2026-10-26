import { writable } from 'svelte/store';
import { ApiError, type ApiErrorShape, type Collection, type PortableImportReport } from '../types';
import { writesBlocked } from '../connectivity';
import { clearOfflineBoards } from '../offlineBoards';

export const pendingMutationCount = writable(0);
export const API_PREFIX = '/api/v1';

export type RequestOptions = Omit<RequestInit, 'body'> & {
  body?: unknown;
  idempotencyKey?: string;
  ifMatch?: string | number;
  /** Optional control-plane calls may observe an intentional app restart. */
  suppressNetworkUnavailable?: boolean;
  /** Optional probes may not invalidate cached read-only board snapshots. */
  preserveOfflineSnapshotsOnError?: boolean;
};

export function pathWithQuery(path: string, query?: Record<string, string | number | boolean | undefined | null>): string {
  const url = new URL(`${API_PREFIX}${path}`, window.location.origin);
  Object.entries(query ?? {}).forEach(([key, value]) => {
    if (value !== undefined && value !== null && String(value).length > 0) {
      url.searchParams.set(key, String(value));
    }
  });
  return `${url.pathname.slice(API_PREFIX.length)}${url.search}`;
}

function asBody(body: unknown): BodyInit | undefined {
  if (body === undefined) return undefined;
  if (typeof body === 'string' || body instanceof FormData || body instanceof Blob) return body;
  return JSON.stringify(body);
}

/**
 * The one low-level request helper used by the UI. Keeping it exported makes
 * the API boundary easy to test without mounting the application.
 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const mutation = !['GET', 'HEAD', 'OPTIONS'].includes((options.method || 'GET').toUpperCase());
  if (mutation) pendingMutationCount.update((count) => count + 1);
  try {
    return await performRequest<T>(path, options);
  } finally {
    if (mutation) pendingMutationCount.update((count) => count - 1);
  }
}

async function performRequest<T>(path: string, options: RequestOptions): Promise<T> {
  const {
    body,
    idempotencyKey,
    ifMatch,
    suppressNetworkUnavailable,
    preserveOfflineSnapshotsOnError,
    ...init
  } = options;
  if (!['GET', 'HEAD', 'OPTIONS'].includes((init.method || 'GET').toUpperCase()) && writesBlocked()) {
    throw new ApiError('Offline mode is read-only. Reconnect before making changes.', 0, 'offline_read_only', {});
  }
  const headers = new Headers(init.headers);
  if (body !== undefined && !(body instanceof FormData) && !(body instanceof Blob)) {
    headers.set('Content-Type', 'application/json');
  }
  headers.set('Accept', 'application/json');
  if (idempotencyKey) headers.set('Idempotency-Key', idempotencyKey);
  if (ifMatch !== undefined) {
    headers.set('If-Match', typeof ifMatch === 'number' ? etagForVersion(ifMatch) : ifMatch);
  }

  let response: Response;
  try {
    response = await fetch(`${API_PREFIX}${path}`, {
      ...init,
      cache: 'no-store',
      body: asBody(body),
      credentials: 'include',
      headers
    });
  } catch (error) {
    // Auth bootstrap handles Access redirects itself. Other unreachable reads
    // and writes enter read-only mode even when navigator.onLine stays true.
    // Failed writes are never retried: the server may already have committed.
    if (error instanceof TypeError && !path.startsWith('/auth/') && !suppressNetworkUnavailable && typeof window !== 'undefined') {
      window.dispatchEvent(new Event('helm:network-unavailable'));
    }
    throw error;
  }

  const text = await response.text();
  let parsed: unknown = undefined;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = text;
    }
  }

  if (!response.ok) {
    if (response.status === 401 || response.status === 403) {
      if (!preserveOfflineSnapshotsOnError) await clearOfflineBoards();
      // A forbidden mutation is a permission failure, not proof that the
      // browser session expired. Only an explicit unauthorized response may
      // invalidate session-bound UI such as the notification inbox.
      if (response.status === 401 && typeof window !== 'undefined') window.dispatchEvent(new Event('helm:auth-invalidated'));
    }
    const envelope = parsed as Partial<ApiErrorShape> | undefined;
    const error = envelope?.error;
    throw new ApiError(
      error?.message || (typeof parsed === 'string' ? parsed : response.statusText) || 'Request failed',
      response.status,
      error?.code || 'request_failed',
      error?.details || {}
    );
  }

  return parsed as T;
}

/**
 * Validation failures are returned by the server in the structured error
 * details alongside the regular message. Keeping the report intact lets the
 * import review dialog show every failing field and remap, not just the first
 * toast string.
 */
export function portableImportReportFromError(error: unknown): PortableImportReport | null {
  if (!(error instanceof ApiError) || !error.details || typeof error.details !== 'object') return null;
  const details = error.details as Partial<PortableImportReport>;
  if (!Array.isArray(details.errors)) return null;
  return {
    format: typeof details.format === 'string' ? details.format : 'helm.portable',
    version: typeof details.version === 'number' ? details.version : 1,
    dry_run: typeof details.dry_run === 'boolean' ? details.dry_run : true,
    conflict: typeof details.conflict === 'string' ? details.conflict : 'remap',
    counts: details.counts && typeof details.counts === 'object' ? details.counts : {},
    remaps: Array.isArray(details.remaps) ? details.remaps : [],
    warnings: Array.isArray(details.warnings) ? details.warnings : [],
    errors: details.errors
  };
}

export function etagForVersion(version: number): string {
  return `"v${version}"`;
}

export function collectionFrom<T>(payload: Collection<T> | T[]): Collection<T> {
  if (Array.isArray(payload)) return { data: payload };
  return { data: payload?.data ?? [], next_cursor: payload?.next_cursor ?? null };
}

export function key(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  return `helm-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export async function collectPages<T>(load: (cursor?: string) => Promise<Collection<T>>, startCursor?: string): Promise<Collection<T>> {
  const data: T[] = [];
  let cursor = startCursor;
  // The database and agent budgets bound practical collection size. This
  // guard still prevents a malformed server cursor from trapping the UI.
  for (let page = 0; page < 100; page += 1) {
    const result = await load(cursor);
    data.push(...result.data);
    if (!result.next_cursor) return { data, next_cursor: null };
    if (result.next_cursor === cursor) {
      throw new ApiError('The server repeated a pagination cursor.', 500, 'invalid_pagination_cursor');
    }
    cursor = result.next_cursor;
  }
  throw new ApiError('The collection exceeded the browser pagination safety limit.', 500, 'pagination_limit');
}
