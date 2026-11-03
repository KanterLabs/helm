import { ApiError, type ApiErrorShape, type PortableImportReport } from '../types';
import { API_PREFIX, pathWithQuery, request } from './client';

/** Download private originals through the signed-in session, without caching. */
export async function exportProjectBundle(projectId: string, signal?: AbortSignal): Promise<Blob> {
  const path = pathWithQuery(`/projects/${encodeURIComponent(projectId)}/export`, { format: 'bundle' });
  const response = await fetch(`${API_PREFIX}${path}`, {
    credentials: 'include', cache: 'no-store', signal, headers: { Accept: 'application/zip' }
  });
  if (!response.ok) {
    if (response.status === 401) window.dispatchEvent(new Event('helm:auth-invalidated'));
    const envelope = await response.json().catch(() => undefined) as ApiErrorShape | undefined;
    throw new ApiError(envelope?.error?.message || 'The project file could not be exported.', response.status,
      envelope?.error?.code || 'export_failed', envelope?.error?.details || {});
  }
  return response.blob();
}

/** The File stays in memory; the browser sends the ZIP without JSON encoding. */
export function importProjectBundle(file: File, projectId: string, dryRun: boolean, idempotencyKey?: string, signal?: AbortSignal) {
  return request<PortableImportReport>(pathWithQuery('/import/bundle', {
    target_project: projectId, dry_run: dryRun, conflict: 'remap'
  }), {
    method: 'POST', body: file, headers: { 'Content-Type': 'application/zip' },
    idempotencyKey: dryRun ? undefined : idempotencyKey, signal
  });
}
