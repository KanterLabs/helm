# Task images for human and agent work

Use task images when a screenshot makes a UI change, visual regression, or
verification result easier to review. Humans can add images from the task's
comment composer. Agents should use the authenticated CLI below. The same task
and project permissions apply to metadata, originals, and PNG thumbnails; there
are no public image URLs. Never paste image bytes or credentials into comments.

## Environment and a live example

Use an installation advertising `GET /api/v1/attachments/capabilities`; the CLI
checks capability limits before staging files. The screenshot implementation is
currently deployed to the private beta. Do not infer production support from a
successful beta test, and keep each environment's credentials separate; see
[authentication](authentication.md#beta-and-production-are-separate-installations).

[HB-2 on beta](https://beta-helm.home.shanekanterman.dev/p/helm-beta/tasks/HB-2?view=activity)
is a live example with a sanitized comparison-view screenshot. Human access
uses the normal private sign-in; the temporary publishing token is not part of
the URL and does not control the attachment's lifetime.

When asked for an example link, inspect existing task evidence first. If needed,
create the example in an authorized project, upload the file, publish it in a
comment/progress update, then verify the Activity entry and original bytes.
An isolated test artifact or a staged upload alone is not a published example.
Use `/p/PROJECT_SLUG/tasks/TASK_KEY?view=activity` for a direct task link.

## Capture and inspect before upload

Capture a synthetic or otherwise shareable view. Inspect the actual image and
redact secrets, browser tokens, credentials, personal data, and private user
content before uploading. Cropping or masking must happen before the upload.
The uploading human or agent owns this review. Helm validates image formats,
dimensions, and size; neither the CLI nor server claims to scan pixels for
secrets. Do not describe an image as scanned or safe based on format validation.
Use concise captions that explain the evidence without repeating sensitive text.

PNG, JPEG, and static WebP are supported, with a default 10 MiB per file and at
most four images per comment or progress update. Server limits can be stricter.
The CLI checks capabilities first. A server without this feature returns a clear
unsupported error; continue with a text update or update the server. Never widen
token scopes or replace credentials to make an upload work.

## Add images to an ordinary comment

Commands below run from the skill directory. Credentials stay in the existing
configuration file or environment, never command arguments. Use one stable
operation ID for a logical update and reuse it unchanged when recovering.

```sh
python3 scripts/helm.py comment \
  --task TC-359 \
  --message "Verified the task image viewer using synthetic content" \
  --image /tmp/helm-proof.png \
  --caption "Task image viewer at desktop size" \
  --commit-sha 0123456789abcdef \
  --build local-verification \
  --viewport-width 1440 --viewport-height 900 \
  --operation-id task-image-review-01
```

Repeat `--image` for multiple files. If captions are supplied, provide exactly
one `--caption` per image, in the same order. Capture metadata applies to every
image in that command. Only the sanitized basename enters upload metadata;
local directories and original bytes never appear in JSON output.

## Attach evidence to structured progress

Use the existing claimed-task progress flow. Images do not change checkpoint
counts, phase, ownership, or the task snapshot rules.

```sh
python3 scripts/helm.py progress \
  --task TC-359 --state verifying --phase "Visual verification" \
  --completed 2 --total 3 \
  --message "Desktop image viewer matches the expected result" \
  --next "Verify the mobile layout" \
  --image /tmp/helm-proof.png --caption "Synthetic desktop proof" \
  --viewport-width 1440 --viewport-height 900 \
  --operation-id task-image-progress-01
```

The CLI stages the complete image batch before posting the update. A local
validation failure or partial batch never publishes an incomplete comment or
progress update. Existing plain-message `progress` remains a legacy text
comment; image evidence requires structured options such as `--state`, or the
ordinary `comment` command.

## Stage, inspect, download, or remove images

An upload creates staged metadata. Staged images become visible in task activity
only when a comment or progress update successfully references them.

```sh
python3 scripts/helm.py attachments upload \
  --task TC-359 --image /tmp/helm-proof.png --caption "Synthetic proof" \
  --operation-id stage-proof-01
python3 scripts/helm.py attachments list --task TC-359
python3 scripts/helm.py attachments status --task TC-359 --attachment ATTACHMENT_ID
python3 scripts/helm.py comment \
  --task TC-359 --message "Reviewed the staged screenshot" \
  --attachment-id ATTACHMENT_ID --operation-id publish-proof-01
python3 scripts/helm.py attachments download \
  --task TC-359 --attachment ATTACHMENT_ID --output /tmp/helm-original.png
python3 scripts/helm.py attachments download \
  --task TC-359 --attachment ATTACHMENT_ID --thumbnail \
  --output /tmp/helm-thumbnail.png
python3 scripts/helm.py attachments delete \
  --task TC-359 --attachment ATTACHMENT_ID --operation-id remove-proof-01
```

`--attachment-id` can also be repeated on structured progress. The combined
number of files and existing IDs must stay at or below four. List includes
published images and the caller's staged images, within existing permissions.
Delete reads the current version and sends `If-Match`. Downloads require a new,
explicit file path, refuse overwrites, stream to a private temporary file, and
verify original size and SHA-256 before installing the result. JSON output
contains metadata; it never streams binary content to stdout.

## Recover a failed update

Keep the original capture files, their order, metadata, and operation ID until
publication succeeds. Each upload uses a stable operation key derived from the
operation ID, file digest, capture metadata, and batch position. A replay must
match the original content and metadata. If a response is lost, the CLI first
checks staged metadata, then replays the same operation key when necessary.

On partial upload or publication failure, JSON errors include
`staged_attachment_ids` and `operation_id`. Inspect those IDs with `status`.
Either retry the entire original command unchanged, publish a deliberately
reviewed selection with `--attachment-id`, or delete unwanted staged images.
Do not assume a transport failure proves publication failed: inspect the task's
activity before creating a new logical update with a new operation ID.

## Repeatable synthetic validation

The focused CLI journey uses an isolated loopback HTTP fixture, an embedded
one-pixel synthetic PNG, and subprocess commands. It verifies complete comment
and progress publication, recovery after a dropped upload response, no partial
publication, capability errors, basename-only multipart data, explicit download
integrity, and versioned deletion. It requires no production service or token.

```sh
HELM_TEST_ARTIFACT=/tmp/helm-image-cli-journey.jsonl \
  python3 -m unittest discover -s scripts -p test_helm_attachments.py -v
```

The optional artifact contains only synthetic command arguments and captured
JSON responses. Review the JSONL file as evidence of the run. Keep test artifacts
free of credentials and private user content.
