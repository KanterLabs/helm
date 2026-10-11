<script lang="ts">
  import { tick } from 'svelte';
  import { api } from '../api';
  import { helpRequest } from '../help';
  import { renderHelpMarkdown } from '../helpMarkdown';

  // In-app help: shows Helm's own user guides (embedded in the server), so
  // "Learn more" works on private installs without internet access.
  let html = '';
  let error = '';
  let loadedPage = '';
  let body: HTMLElement;
  let closeButton: HTMLButtonElement;

  $: request = $helpRequest;
  $: if (request) void show(request.page, request.anchor || '');

  async function show(page: string, anchor: string) {
    error = '';
    if (page !== loadedPage) {
      try {
        html = renderHelpMarkdown(await api.getHelpDocument(page));
        loadedPage = page;
      } catch {
        error = 'This help page could not be loaded.';
        return;
      }
    }
    await tick();
    closeButton?.focus();
    const target = anchor ? body?.querySelector(`#${CSS.escape(anchor)}`) : null;
    if (target) target.scrollIntoView({ block: 'start' });
    else body?.scrollTo({ top: 0 });
  }

  function close() {
    helpRequest.set(null);
  }

  function navigate(event: MouseEvent) {
    const link = (event.target as HTMLElement).closest('a[data-help-anchor]') as HTMLAnchorElement | null;
    if (!link) return;
    event.preventDefault();
    helpRequest.set({ page: link.dataset.helpPage || loadedPage, anchor: link.dataset.helpAnchor || '' });
  }
</script>

<svelte:window on:keydown={(event) => { if (request && event.key === 'Escape') close(); }} />

{#if request}
  <div class="help-backdrop" role="presentation" on:click={close}></div>
  <div class="help-drawer" role="dialog" aria-modal="true" aria-label="Help" data-help-drawer>
    <header>
      <span>Help</span>
      <button bind:this={closeButton} class="text-button" type="button" on:click={close}>Close</button>
    </header>
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_noninteractive_element_interactions -->
    <article bind:this={body} class="help-body" on:click={navigate}>
      {#if error}<p class="help-error" role="alert">{error}</p>{:else}{@html html}{/if}
    </article>
  </div>
{/if}

<style>
  .help-backdrop { position: fixed; inset: 0; z-index: 80; background: rgb(15 18 30 / 0.28); }
  .help-drawer { position: fixed; top: 0; right: 0; bottom: 0; z-index: 81; display: grid; grid-template-rows: auto 1fr; width: min(640px, 100vw); border-left: 1px solid var(--border); background: var(--surface); box-shadow: -12px 0 32px rgb(15 18 30 / 0.18); }
  header { display: flex; align-items: center; justify-content: space-between; padding: 12px 18px; border-bottom: 1px solid var(--border); font-weight: 800; }
  .help-body { overflow-y: auto; padding: 8px 22px 40px; font-size: 14px; line-height: 1.6; color: var(--ink); }
  .help-body :global(h2) { margin: 18px 0 10px; font-size: 21px; }
  .help-body :global(h3) { margin: 26px 0 8px; font-size: 17px; scroll-margin-top: 8px; }
  .help-body :global(h4) { margin: 20px 0 6px; font-size: 15px; }
  .help-body :global(p), .help-body :global(ul), .help-body :global(ol) { margin: 8px 0; }
  .help-body :global(li) { margin: 3px 0; }
  .help-body :global(code) { padding: 1px 4px; border-radius: 4px; background: var(--surface-muted); font-size: 12.5px; overflow-wrap: anywhere; }
  .help-body :global(pre) { overflow-x: auto; padding: 10px 12px; border-radius: 8px; background: var(--surface-muted); }
  .help-body :global(pre code) { padding: 0; background: none; }
  .help-body :global(.help-table) { overflow-x: auto; }
  .help-body :global(table) { width: 100%; margin: 10px 0; border-collapse: collapse; font-size: 13px; }
  .help-body :global(th), .help-body :global(td) { padding: 6px 8px; border-bottom: 1px solid var(--border); text-align: left; vertical-align: top; }
  .help-body :global(th) { color: var(--muted); font-size: 11.5px; text-transform: uppercase; letter-spacing: 0.03em; }
  .help-body :global(a) { color: var(--purple); font-weight: 600; }
  .help-error { color: var(--semantic-red); }
</style>
