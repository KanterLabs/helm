import { api } from '../../api';
import { key } from '../../api/client';
import type { AttachmentCapabilities, ScreenshotDraft } from '../../types';
import { drawer } from '../../../app/state/drawer.svelte';

function update(id: string, change: Partial<ScreenshotDraft>) {
  drawer.screenshotDrafts = drawer.screenshotDrafts.map((draft) => draft.key === id ? { ...draft, ...change } : draft);
}

export async function uploadScreenshot(draft: ScreenshotDraft) {
  if (!draft.file || !drawer.screenshotDrafts.some((item) => item.key === draft.key)) return;
  const controller = new AbortController();
  update(draft.key, { state: 'uploading', error: '', controller });
  try {
    const attachment = await api.uploadAttachment(draft.taskId, draft.file, draft.key, controller.signal);
    // Removed drafts, task switches, and logout cannot revive a completed upload.
    update(draft.key, { attachment, state: 'ready', controller: undefined });
  } catch (error) {
    update(draft.key, { state: controller.signal.aborted ? 'cancelled' : 'error', controller: undefined,
      error: controller.signal.aborted ? 'Upload cancelled.' : error instanceof Error ? error.message : 'Upload failed. Please retry.' });
  }
}

export function queueScreenshots(files: File[], capabilities: AttachmentCapabilities): string {
  const taskId = drawer.drawerTask?.id;
  if (!taskId || !files.length) return '';
  if (drawer.commentSending) return 'Wait for this update to finish sending.';
  if (files.length + drawer.screenshotDrafts.length > capabilities.max_images_per_update) {
    return `Add up to ${capabilities.max_images_per_update} screenshots per update. No files were added.`;
  }
  const invalid = files.find((file) => !capabilities.types.includes(file.type) || file.size > capabilities.max_file_bytes);
  if (invalid) return `${invalid.name}: choose PNG, JPEG or static WebP within ${Math.round(capabilities.max_file_bytes / 1048576)} MB. No files were added.`;
  const drafts: ScreenshotDraft[] = files.map((file) => ({ key: key(), taskId, file, preview: URL.createObjectURL(file), caption: '', state: 'uploading', error: '' }));
  drawer.screenshotDrafts = [...drawer.screenshotDrafts, ...drafts];
  drafts.forEach((draft) => void uploadScreenshot(draft));
  return '';
}

export function editScreenshotCaption(id: string, caption: string) {
  update(id, { caption });
}

export function moveScreenshot(id: string, direction: number) {
  const drafts = [...drawer.screenshotDrafts];
  const from = drafts.findIndex((item) => item.key === id);
  const to = from + direction;
  if (from < 0 || to < 0 || to >= drafts.length) return;
  [drafts[from], drafts[to]] = [drafts[to], drafts[from]];
  drawer.screenshotDrafts = drafts;
}

export function removeScreenshot(id: string) {
  const draft = drawer.screenshotDrafts.find((item) => item.key === id);
  draft?.controller?.abort();
  if (draft?.preview) URL.revokeObjectURL(draft.preview);
  drawer.screenshotDrafts = drawer.screenshotDrafts.filter((item) => item.key !== id);
  if (draft?.attachment) void api.deleteAttachment(draft.attachment).catch(() => undefined);
}

export function clearScreenshotDrafts() {
  for (const draft of drawer.screenshotDrafts) {
    draft.controller?.abort();
    if (draft.preview) URL.revokeObjectURL(draft.preview);
  }
  drawer.screenshotDrafts = [];
  drawer.screenshotCommentKey = '';
  drawer.screenshotCommentFingerprint = '';
}

/** Persist every caption before attempting the single atomic publish request. */
export async function prepareScreenshots(): Promise<string[]> {
  const drafts = [...drawer.screenshotDrafts];
  if (drafts.some((draft) => draft.state !== 'ready' || !draft.attachment)) throw new Error('Finish uploading or remove every failed screenshot before sending.');
  for (const draft of drafts) {
    if (draft.attachment && draft.caption !== (draft.attachment.caption || '')) {
      const attachment = await api.patchAttachment(draft.attachment, draft.caption);
      update(draft.key, { attachment });
    }
  }
  return drafts.map((draft) => draft.attachment!.id);
}
