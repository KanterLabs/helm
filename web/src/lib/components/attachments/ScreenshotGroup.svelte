<script lang="ts">
  import type { Attachment } from '../../types';
  import PrivateImage from './PrivateImage.svelte';
  import ScreenshotViewer from './ScreenshotViewer.svelte';
  export let images: Attachment[] = [];
  export let currentActorId = '';
  export let canManage = false;
  let selected: number | null = null;
  let deleted = new Set<string>();
  $: available = images.filter((image) => !deleted.has(image.id));
</script>

{#if available.length}
  <div class="screenshot-group" aria-label="Screenshot evidence">
    {#each available as image, index (image.id)}
      <button class="screenshot-card" type="button" aria-label={`Open screenshot: ${image.caption || image.filename}`} on:click={() => selected = index}>
        <span class="screenshot-thumbnail"><PrivateImage {image} thumbnail /></span>
        <span class="screenshot-card-caption">{image.caption || image.filename}</span>
      </button>
    {/each}
  </div>
  {#if selected !== null}<ScreenshotViewer images={available} {selected} {currentActorId} {canManage} onClose={() => selected = null} onDeleted={(id) => { deleted = new Set([...deleted, id]); }} />{/if}
{/if}
