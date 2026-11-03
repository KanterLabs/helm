# Task screenshots and image evidence

Helm keeps screenshots beside the comment or progress update they explain.
Upload an image first, then publish its ID with an update. Uploading alone
stages the image; publishing links it to canonical task history atomically.
A later progress pulse does not replace images on earlier progress entries.

## Human workflow

In a task's comment composer, paste or drop a screenshot, or choose an image
file. Review its preview, add a caption if useful, and publish the comment.
A comment can contain images without text. Activity shows thumbnails; open
an image at full size to inspect details or compare evidence from successive
updates. Commit, build and viewport metadata provide context when supplied.

Before publishing, edit captions or reorder the staged images in the composer.
The image creator or a human administrator can delete a published image.
Metadata edits after publication are available through the API. Deletion makes its content unavailable immediately. Deleting the
containing comment also hides its images. Physical cleanup follows retention;
there is no public image recovery endpoint. Operators should follow
[attachment backup and recovery procedures](ATTACHMENT_OPERATIONS.md).

## Agent workflow

Use the authenticated API with task/project access. Read operations require
`tasks:read`; image upload and metadata mutations require `tasks:write`.
Publishing progress retains the existing active-claim, `tasks:claim` and task
`If-Match` requirements. Keep tokens out of screenshots and captured artifacts.

1. Read `GET /api/v1/attachments/capabilities` for supported types and limits.
2. Stage a file with `POST /api/v1/tasks/{task}/attachments` using
   `multipart/form-data`, a `file` part and a stable `Idempotency-Key`.
   Optional fields are `caption`, `commit_sha`, `build`, `viewport_width`
   and `viewport_height`. Send bytes directly; Helm does not fetch public URLs.
3. Keep the returned attachment `id`. New uploads return 201; identical
   retries return 200 with the same ID. Reusing a key with different input
   returns 409. Keep the key stable across uncertain upload retries.
4. Publish `attachment_ids: ["<id>"]` with a comment or progress request.
   IDs must identify eligible staged images on this task. A failed publication
   leaves the images staged so the update can be corrected and retried.
5. Read the task timeline or comments to inspect the published `attachments`.
   Images belong to that history entry, not the mutable latest work snapshot.

For example, after staging an image, an image-only comment is:

```json
{"attachment_ids":["<returned-attachment-id>"]}
```

A progress request still includes its required `operation_id`, `state` and
`summary`; add `attachment_ids` alongside those fields. Use the current task
ETag for progress, and the attachment metadata ETag for editing/deleting an
attachment. These are separate versioned resources.

## API resources and privacy

| Resource | Behavior |
| --- | --- |
| `GET /api/v1/tasks/{task}/attachments` | Cursor-paginated published images. Add `staged=true` for your staged images; human administrators can inspect all staged images. |
| `GET /api/v1/tasks/{task}/attachments/{id}` | Metadata plus a version ETag. |
| `PATCH /api/v1/tasks/{task}/attachments/{id}` | Partial caption, commit, build or viewport metadata update; requires `If-Match`. |
| `DELETE /api/v1/tasks/{task}/attachments/{id}` | Tombstone an image; requires `If-Match`. |
| `GET /api/v1/tasks/{task}/attachments/{id}/content` | Original image bytes. |
| `GET /api/v1/tasks/{task}/attachments/{id}/thumbnail` | Generated PNG preview. |

Content and thumbnail requests authenticate and enforce project visibility.
They use private authenticated URLs and `Cache-Control: no-store`; possession
of a URL does not grant access. Staged images are restricted to their creator
and human administrators. Content supports HEAD, conditional requests and
byte ranges. A stale metadata `If-Match` returns 409; omission returns 428.

## Limits and operations

The default limits are 10 MiB per file, 40 million decoded pixels, 8,192 pixels
per dimension, four images per update and 1 GiB of retained images per project.
Only static PNG, JPEG and WebP are accepted. Validation checks image bytes and
decoded dimensions. The upload streams under its own limits rather than the
generic 2 MiB JSON request limit. Metadata is bounded: caption 2,000 UTF-8
bytes, commit 128 bytes, build 200 bytes and viewport dimensions 0–100,000.
Operators can set `HELM_ATTACHMENT_MAX_FILE_BYTES` (up to 40 MiB) and
`HELM_ATTACHMENT_PROJECT_QUOTA_BYTES` (up to 1 TiB). See
[configuration](CONFIGURATION.md). The capabilities response is the
authoritative source of deployment limits.

Images live beside the database in its `attachments` directory. A database-only
copy cannot recover image bytes. See [attachment operations](ATTACHMENT_OPERATIONS.md)
for retention, consistent backups, verification and restore.

## Portable transfer

Use `GET /api/v1/export?format=bundle` or the project export equivalent for
an image-bearing `helm.portable.bundle` v5 ZIP. It contains a v4 JSON archive,
a checksummed manifest and published originals. Staged, deleted and images
hidden by deleted comments stay local. Thumbnail files are regenerated.
Legacy JSON export continues to work and warns when image evidence is omitted.

Import a raw ZIP using `POST /api/v1/import/bundle` with `Content-Type:
application/zip`. The existing import query options apply: `dry_run=true`,
`conflict=remap|fail` and `target_project`. Validate with `dry_run=true` before
applying to a destination. Import validates checksums, images and history links
before inserting records; destination records are never overwritten. Bundle
import uses the existing portable import report, including attachment ID
remaps, and does not restore credentials or executable jobs.

See [the OpenAPI source](../openapi.yaml) for schemas and
[portable transfer](PORTABILITY.md) for the wider import/export workflow.
