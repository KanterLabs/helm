import { ApiError, type Attachment, type AttachmentCapabilities, type Collection } from '../types';
import { API_PREFIX, collectPages, collectionFrom, key, pathWithQuery, request } from './client';

const base = (task: string) => `/tasks/${encodeURIComponent(task)}/attachments`;
const resource = (image: Attachment) => `${base(image.task_id)}/${encodeURIComponent(image.id)}`;

export const attachmentsApi = {
  attachmentCapabilities: () => request<AttachmentCapabilities>('/attachments/capabilities'),
  listAttachments: (task: string, staged = false, cursor?: string) =>
    request<Collection<Attachment> | Attachment[]>(pathWithQuery(base(task), { staged: staged || undefined, cursor, limit: 100 })).then(collectionFrom),
  listAllAttachments: (task: string) => collectPages((cursor) =>
    request<Collection<Attachment> | Attachment[]>(pathWithQuery(base(task), { cursor, limit: 100 })).then(collectionFrom)),
  uploadAttachment: (task: string, file: File, idempotencyKey = key(), signal?: AbortSignal) => {
    const body = new FormData();
    body.append('file', file);
    return request<Attachment>(base(task), { method: 'POST', body, signal, idempotencyKey });
  },
  patchAttachment: (image: Attachment, caption: string) => request<Attachment>(resource(image), {
    method: 'PATCH', body: { caption }, ifMatch: image.version, idempotencyKey: key()
  }),
  deleteAttachment: (image: Attachment) => request<void>(resource(image), {
    method: 'DELETE', ifMatch: image.version, idempotencyKey: key()
  })
};

/** No binary enters local storage, the offline board cache, or public URLs. */
export async function attachmentBlob(image: Attachment, thumbnail = false, signal?: AbortSignal): Promise<Blob> {
  const response = await fetch(`${API_PREFIX}${resource(image)}/${thumbnail ? 'thumbnail' : 'content'}`, {
    credentials: 'include', cache: 'no-store', signal, headers: { Accept: 'image/*' }
  });
  if (!response.ok) {
    if (response.status === 401) window.dispatchEvent(new Event('helm:auth-invalidated'));
    throw new ApiError(response.status === 404 || response.status === 410
      ? 'This screenshot is no longer available.' : 'This screenshot could not be loaded. Check your access and retry.', response.status, 'image_unavailable');
  }
  return response.blob();
}
