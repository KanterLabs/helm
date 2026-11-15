<script lang="ts">
  import { onMount } from 'svelte';
  import { githubUrl } from './connection';

  /** Where the administrator can revoke Helm on GitHub (C5 `authorizations_url`). */
  export let authorizationsUrl: string | undefined;
  export let busy = false;
  export let onConfirm: () => void;
  export let onKeep: () => void;

  let heading: HTMLElement;
  $: revokeUrl = githubUrl(authorizationsUrl);

  onMount(() => heading?.focus());

  function keydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && !busy) { event.stopPropagation(); onKeep(); }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div class="confirm" role="group" aria-labelledby="github-disconnect-heading" data-github-disconnect-confirm on:keydown={keydown}>
  <h4 id="github-disconnect-heading" tabindex="-1" bind:this={heading}>Disconnect GitHub?</h4>
  <p>Helm deletes its GitHub sign-in and stops reading your repositories. Pull request status on cards stops updating until someone connects again.</p>
  <p>Helm cannot remove its access on GitHub by itself. To remove it there too, open your
    {#if revokeUrl}<a href={revokeUrl} target="_blank" rel="noopener noreferrer">authorized GitHub Apps<span class="sr-only"> (opens in a new tab)</span></a>{:else}authorized GitHub Apps in your GitHub settings{/if}
    and revoke Helm.</p>
  <div class="actions">
    <button class="button danger" type="button" disabled={busy} on:click={onConfirm}>{#if busy}<span class="button-spinner"></span>{/if}Disconnect GitHub</button>
    <button class="button quiet-button" type="button" disabled={busy} on:click={onKeep}>Keep connected</button>
  </div>
</div>

<style>
  .confirm { display: grid; gap: 10px; padding: 14px 16px; border: 1px solid color-mix(in srgb, var(--red), var(--border) 65%); border-radius: 10px; background: var(--red-soft); }
  h4 { margin: 0; font-size: var(--text-sm); outline: none; }
  p { margin: 0; color: var(--ink-soft); font-size: var(--text-sm); line-height: 1.5; }
  a { color: var(--purple); font-weight: 600; }
  .actions { display: flex; flex-wrap: wrap; gap: 10px; }
  .actions .button { min-height: 40px; }
  /* A fixed deep red keeps white text readable in both themes. */
  .danger { color: #fff; border-color: #a12535; background: #b42335; }
  .danger:hover:not(:disabled) { background: #a12535; }
</style>
