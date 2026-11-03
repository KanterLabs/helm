<script lang="ts">
  import { onDestroy } from 'svelte';
  import { attachmentBlob } from '../../api/attachments';
  import type { Attachment } from '../../types';
  export let image: Attachment;
  export let thumbnail = false;
  export let pixelated = false;
  let url = '';
  let error = '';
  let loading = false;
  let controller: AbortController | undefined;
  let current = '';
  $: identity = `${image.task_id}:${image.id}:${image.version}:${thumbnail}`;
  $: if (identity !== current) { current = identity; void load(); }
  async function load() {
    controller?.abort();
    if (url) URL.revokeObjectURL(url);
    url = '';
    error = '';
    if (image.status === 'deleted') { error = 'This screenshot was deleted.'; return; }
    const request = new AbortController();
    controller = request;
    loading = true;
    try {
      const blob = await attachmentBlob(image, thumbnail, request.signal);
      if (!request.signal.aborted) url = URL.createObjectURL(blob);
    } catch (failure) {
      if (!request.signal.aborted) error = failure instanceof Error ? failure.message : 'Screenshot unavailable.';
    } finally { if (!request.signal.aborted) loading = false; }
  }
  onDestroy(() => { controller?.abort(); if (url) URL.revokeObjectURL(url); });
</script>

{#if url}<img src={url} alt={image.caption || image.filename} class:pixelated draggable="false" />
{:else if error}<span class="screenshot-missing" role="status">{error}{#if !thumbnail}<button type="button" class="text-button" on:click={() => void load()}>Retry image</button>{/if}</span>
{:else if loading}<span class="screenshot-loading" role="status">Loading screenshot…</span>{/if}
