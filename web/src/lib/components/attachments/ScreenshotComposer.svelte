<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../api';
  import type { AttachmentCapabilities } from '../../types';
  import { drawer } from '../../../app/state/drawer.svelte';
  import { editScreenshotCaption, moveScreenshot, queueScreenshots, removeScreenshot, uploadScreenshot } from './drafts';

  let capabilities: AttachmentCapabilities | null = null;
  let error = '';
  let dragging = false;
  let picker: HTMLInputElement;
  onMount(() => {
    let active = true;
    void api.attachmentCapabilities().then((value) => { if (active) capabilities = value; })
      .catch(() => { if (active) error = 'Screenshots are unavailable right now. Your note can still be sent.'; });
    return () => { active = false; };
  });
  function add(files: File[]) {
    if (!capabilities?.enabled) return;
    error = queueScreenshots(files, capabilities);
  }
  function paste(event: ClipboardEvent) {
    const files = Array.from(event.clipboardData?.items || []).filter((item) => item.kind === 'file').map((item) => item.getAsFile()).filter((file): file is File => Boolean(file));
    if (files.length) { event.preventDefault(); add(files); }
  }
  function drop(event: DragEvent) {
    event.preventDefault();
    dragging = false;
    add(Array.from(event.dataTransfer?.files || []));
  }
</script>

<!-- The drop target is also keyboard reachable through its visible picker. -->
<div class="screenshot-composer" class:dragging on:paste={paste} on:dragover|preventDefault={() => dragging = true} on:dragleave={() => dragging = false} on:drop={drop} role="group" aria-label="Screenshot attachments">
  <slot />
  <div class="screenshot-composer-tools">
    <input bind:this={picker} data-screenshot-picker class="sr-only" type="file" multiple accept="image/png,image/jpeg,image/webp" tabindex="-1" disabled={!capabilities?.enabled || drawer.commentSending} on:change={() => { add(Array.from(picker.files || [])); picker.value = ''; }} />
    <button class="text-button" type="button" disabled={!capabilities?.enabled || drawer.commentSending} on:click={() => picker.click()}>Add screenshots</button>
    <span>{capabilities?.enabled ? 'Paste or drop images here' : capabilities ? 'Screenshots are turned off' : 'Checking screenshots…'}</span>
  </div>
  {#if error}<p class="screenshot-error" role="alert">{error}</p>{/if}
  {#if drawer.screenshotDrafts.length}
    <ol class="screenshot-drafts" aria-label="Screenshots to send">
      {#each drawer.screenshotDrafts as draft, index (draft.key)}
        <li class="screenshot-draft" data-screenshot-state={draft.state}>
          {#if draft.preview}<img class="screenshot-draft-preview" src={draft.preview} alt="" />{/if}
          <div class="screenshot-draft-content">
            <strong title={draft.file?.name || draft.attachment?.filename}>{draft.file?.name || draft.attachment?.filename}</strong>
            <span role="status">{draft.state === 'ready' ? 'Ready to send' : draft.state === 'uploading' ? 'Uploading…' : draft.error}</span>
            <input type="text" aria-label={`Caption for ${draft.file?.name || draft.attachment?.filename}`} placeholder="Describe this screenshot…" maxlength="2000" value={draft.caption} disabled={drawer.commentSending} on:input={(event) => editScreenshotCaption(draft.key, event.currentTarget.value)} />
            <div class="screenshot-draft-actions">
              {#if draft.state === 'error' || draft.state === 'cancelled'}<button class="text-button" type="button" disabled={drawer.commentSending} on:click={() => void uploadScreenshot(draft)}>Retry upload</button>{/if}
              {#if draft.state === 'uploading'}<button class="text-button" type="button" on:click={() => draft.controller?.abort()}>Cancel upload</button>{/if}
              <button class="text-button" type="button" aria-label={`Move screenshot ${index + 1} earlier`} disabled={index === 0 || drawer.commentSending} on:click={() => moveScreenshot(draft.key, -1)}>←</button>
              <button class="text-button" type="button" aria-label={`Move screenshot ${index + 1} later`} disabled={index === drawer.screenshotDrafts.length - 1 || drawer.commentSending} on:click={() => moveScreenshot(draft.key, 1)}>→</button>
              <button class="text-button" type="button" disabled={drawer.commentSending} aria-label={`Remove ${draft.file?.name || draft.attachment?.filename}`} on:click={() => removeScreenshot(draft.key)}>Remove</button>
            </div>
          </div>
        </li>
      {/each}
    </ol>
  {/if}
</div>
