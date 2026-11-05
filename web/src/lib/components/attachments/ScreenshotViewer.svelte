<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../../api';
  import { attachmentBlob } from '../../api/attachments';
  import type { Attachment } from '../../types';
  import PrivateImage from './PrivateImage.svelte';
  export let images: Attachment[];
  export let selected = 0;
  export let currentActorId = '';
  export let canManage = false;
  export let onClose: () => void;
  export let onDeleted: (id: string) => void = () => undefined;
  let dialog: HTMLDialogElement;
  let returnFocus: HTMLElement | null = null;
  let compare = false;
  let compareId = images.find((image, index) => index !== selected)?.id || '';
  let pixelated = false;
  let zoom = 100;
  let error = '';
  let busy = false;
  let downloadUrl = '';
  let downloadController: AbortController | undefined;
  $: image = images[selected];
  $: second = images.find((candidate) => candidate.id === compareId && candidate.id !== image?.id);
  onMount(() => {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialog.showModal();
    dialog.querySelector<HTMLButtonElement>('[data-viewer-close]')?.focus();
  });
  onDestroy(() => {
    downloadController?.abort();
    if (downloadUrl) URL.revokeObjectURL(downloadUrl);
    dialog?.close();
    returnFocus?.focus();
  });
  function navigate(direction: number) {
    selected = (selected + direction + images.length) % images.length;
    if (images[selected].id === compareId) compareId = images.find((candidate) => candidate.id !== images[selected].id)?.id || '';
    error = '';
  }
  function keyboard(event: KeyboardEvent) {
    // The underlying drawer also handles Escape. Keep this modal independent.
    event.stopPropagation();
    if (event.key === 'Escape') { event.preventDefault(); onClose(); }
    if (event.target instanceof HTMLInputElement || event.target instanceof HTMLSelectElement) return;
    if (event.key === 'ArrowLeft') { event.preventDefault(); navigate(-1); }
    if (event.key === 'ArrowRight') { event.preventDefault(); navigate(1); }
  }
  async function download() {
    if (busy) return;
    busy = true;
    error = '';
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const selectedImage = image;
    const controller = new AbortController();
    downloadController = controller;
    try {
      const blob = await attachmentBlob(selectedImage, false, controller.signal);
      if (controller.signal.aborted) return;
      if (downloadUrl) URL.revokeObjectURL(downloadUrl);
      downloadUrl = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = downloadUrl;
      anchor.download = selectedImage.filename;
      anchor.click();
      if (dialog.open && document.activeElement === document.body) trigger?.focus();
    } catch (failure) { if (!controller.signal.aborted) error = failure instanceof Error ? failure.message : 'Download failed.'; }
    finally { busy = false; }
  }
  async function remove() {
    if (!window.confirm('Delete this screenshot? Its image will no longer be available in task history.')) return;
    busy = true;
    error = '';
    try { await api.deleteAttachment(image); onDeleted(image.id); onClose(); }
    catch (failure) { error = failure instanceof Error ? failure.message : 'The screenshot could not be deleted.'; }
    finally { busy = false; }
  }
  function context(value: Attachment) {
    return [value.filename, `${value.width} × ${value.height}`, value.commit_sha ? `Commit ${value.commit_sha}` : '', value.build ? `Build ${value.build}` : '', value.viewport_width && value.viewport_height ? `Viewport ${value.viewport_width} × ${value.viewport_height}` : '', new Date(value.created_at).toLocaleString()].filter(Boolean).join(' · ');
  }
</script>

<dialog bind:this={dialog} class="screenshot-viewer" aria-label="Screenshot viewer" on:keydown={keyboard} on:cancel|preventDefault={onClose}>
  <div class="screenshot-viewer-header"><h2>Screenshot viewer</h2><button data-viewer-close class="icon-button" type="button" aria-label="Close screenshot viewer" on:click={onClose}>×</button></div>
  {#if image}
    <div class="screenshot-viewer-tools">
      <button class="button quiet-button" type="button" disabled={images.length < 2} on:click={() => navigate(-1)} aria-label="Previous screenshot">←</button>
      <span aria-live="polite">{selected + 1} of {images.length}</span>
      <button class="button quiet-button" type="button" disabled={images.length < 2} on:click={() => navigate(1)} aria-label="Next screenshot">→</button>
      <label>Zoom <select bind:value={zoom}><option value={100}>Fit</option><option value={150}>150%</option><option value={200}>200%</option><option value={400}>400%</option></select></label>
      <label><input type="checkbox" bind:checked={pixelated} /> Crisp pixels</label>
      {#if images.length > 1}<label><input type="checkbox" bind:checked={compare} /> Compare</label>{/if}
      <button class="text-button" type="button" disabled={busy} on:click={() => void download()}>Download original</button>
      {#if image.actor_id === currentActorId || canManage}<button class="text-button danger-text-button" type="button" disabled={busy} on:click={() => void remove()}>Delete screenshot</button>{/if}
    </div>
    {#if error}<p class="screenshot-error" role="alert">{error}</p>{/if}
    {#if compare}<label class="screenshot-compare-choice">Compare with <select bind:value={compareId}>{#each images.filter((candidate) => candidate.id !== image.id) as candidate}<option value={candidate.id}>{candidate.caption || candidate.filename}</option>{/each}</select></label>{/if}
    <div class="screenshot-compare" class:comparing={compare && Boolean(second)}>
      <figure><figcaption>{#if compare}<strong>Before</strong>{/if}<span>{image.caption || image.filename}</span><small>{context(image)}</small></figcaption><div class="screenshot-canvas"><div style={`width:${zoom}%;min-width:100%`}><PrivateImage {image} {pixelated} /></div></div></figure>
      {#if compare && second}<figure><figcaption><strong>After</strong><span>{second.caption || second.filename}</span><small>{context(second)}</small></figcaption><div class="screenshot-canvas"><div style={`width:${zoom}%;min-width:100%`}><PrivateImage image={second} {pixelated} /></div></div></figure>{/if}
    </div>
  {/if}
</dialog>
