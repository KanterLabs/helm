/** Private screenshot metadata. Binary content is fetched separately in memory. */
export interface Attachment {
  id: string;
  task_id: string;
  project_id: string;
  actor_id: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  sha256: string;
  width: number;
  height: number;
  status: 'staged' | 'published' | 'deleted';
  caption: string;
  commit_sha: string;
  build: string;
  viewport_width?: number | null;
  viewport_height?: number | null;
  version: number;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
}

export interface AttachmentCapabilities {
  enabled: boolean;
  types: string[];
  max_file_bytes: number;
  max_pixels: number;
  max_dimension: number;
  max_images_per_update: number;
  project_quota_bytes: number;
}

export interface ScreenshotDraft {
  key: string;
  taskId: string;
  file?: File;
  preview?: string;
  attachment?: Attachment;
  caption: string;
  state: 'uploading' | 'ready' | 'error' | 'cancelled';
  error: string;
  controller?: AbortController;
}
