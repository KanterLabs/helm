<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../api';
  import type { Attachment } from '../../types';
  import ScreenshotGroup from './ScreenshotGroup.svelte';
  export let taskId: string;
  export let currentActorId = '';
  export let canManage = false;
  export let refresh = 0;
  let images: Attachment[] = [];
  let opened = false;
  let loading = false;
  let error = '';
  let request = 0;
  let mounted = false;
  let lastRefresh = -1;
  onMount(() => { mounted = true; return () => { mounted = false; request += 1; }; });
  $: if (mounted && opened && refresh !== lastRefresh) { lastRefresh = refresh; void load(); }
  async function load() {
    const current = ++request;
    const owner = taskId;
    loading = true;
    error = '';
    try {
      const result = await api.listAllAttachments(owner);
      if (mounted && current === request && taskId === owner) images = result.data.filter((image) => image.status === 'published' || image.status === 'deleted');
    } catch (failure) { if (mounted && current === request) error = failure instanceof Error ? failure.message : 'Screenshots could not be loaded.'; }
    finally { if (mounted && current === request) loading = false; }
  }
</script>

<div class="screenshot-gallery">
  <button class="text-button" type="button" aria-expanded={opened} on:click={() => { lastRefresh = -1; opened = !opened; }}>All task screenshots</button>
  {#if opened}
    {#if loading}<p role="status">Loading screenshots…</p>{/if}
    {#if error}<p class="screenshot-error" role="alert">{error}<button class="text-button" type="button" on:click={() => void load()}>Retry gallery</button></p>
    {:else if !loading && !images.length}<p>No published screenshots yet.</p>{/if}
    <ScreenshotGroup {images} {currentActorId} {canManage} />
  {/if}
</div>
