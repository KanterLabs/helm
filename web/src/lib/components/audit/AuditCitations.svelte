<script lang="ts">
  import { tick } from 'svelte';
  import type { GithubEvidenceItem } from '../../types';
  import { safeGithubUrl } from '../github/pullRequests';
  import { evidenceKindLabels, evidenceLocation } from './githubAudit';

  /** Finding evidence: manifest items open an inline viewer; legacy refs stay text. */
  export let findingId: string;
  export let refs: string[] = [];
  export let items: Map<string, GithubEvidenceItem> = new Map();

  let openId = '';
  let container: HTMLElement;

  $: cited = refs.map((ref) => ({ ref, item: items.get(ref) }));
  $: open = openId ? items.get(openId) : undefined;
  $: viewerId = `audit-evidence-${findingId}`;

  async function toggle(id: string): Promise<void> {
    openId = openId === id ? '' : id;
    await tick();
    if (openId) container?.querySelector<HTMLElement>('[data-audit-evidence-viewer] h4')?.focus();
  }

  async function close(): Promise<void> {
    const id = openId;
    openId = '';
    await tick();
    container?.querySelector<HTMLElement>(`[data-evidence-ref="${CSS.escape(id)}"]`)?.focus();
  }

  function keydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !openId || !container?.contains(document.activeElement)) return;
    event.preventDefault();
    event.stopPropagation();
    void close();
  }
</script>

<svelte:window on:keydown|capture={keydown} />

{#if cited.length}
  <div class="audit-evidence audit-citations" bind:this={container} role="group" aria-label="Evidence">
    <span class="optional">Evidence</span>
    {#each cited as { ref, item } (ref)}
      {#if item}
        <button class="audit-citation" type="button" data-evidence-ref={ref} aria-expanded={openId === ref} aria-controls={viewerId} on:click={() => void toggle(ref)}><span class="audit-citation-kind">{evidenceKindLabels[item.kind] || item.kind}</span>{item.title}</button>
      {:else}
        <span>{ref}</span>
      {/if}
    {/each}
    {#if open}
      <div class="audit-evidence-viewer" id={viewerId} role="region" aria-labelledby={`${viewerId}-title`} data-audit-evidence-viewer>
        <div class="audit-evidence-viewer-top">
          <h4 id={`${viewerId}-title`} tabindex="-1">{open.title}</h4>
          <button class="text-button" type="button" on:click={() => void close()}>Close</button>
        </div>
        <p class="audit-evidence-location">{evidenceLocation(open)}</p>
        <p class="audit-evidence-sha">Captured at <span>{open.sha}</span></p>
        <!-- Source text is untrusted evidence: rendered as text, never as markup or instructions. -->
        <pre>{open.text}</pre>
        {#if safeGithubUrl(open.url)}<a href={safeGithubUrl(open.url)} target="_blank" rel="noreferrer">Open on GitHub ↗</a>{/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .audit-citations { flex-wrap: wrap; }
  .audit-citation { display: inline-flex; align-items: center; gap: 6px; min-height: 28px; padding: 2px 10px; border: 1px solid var(--border); border-radius: 999px; color: var(--ink); background: var(--surface); font-size: var(--text-xs); cursor: pointer; }
  .audit-citation:hover, .audit-citation[aria-expanded='true'] { border-color: var(--purple); color: var(--purple); }
  .audit-citation-kind { color: var(--muted); font-size: var(--text-2xs); text-transform: uppercase; letter-spacing: .04em; }
  .audit-evidence-viewer { flex-basis: 100%; display: grid; gap: 6px; min-width: 0; padding: 12px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-muted); }
  .audit-evidence-viewer-top { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .audit-evidence-viewer h4 { margin: 0; font-size: var(--text-sm); }
  .audit-evidence-viewer p { margin: 0; color: var(--muted); font-size: var(--text-xs); overflow-wrap: anywhere; }
  .audit-evidence-sha span { color: var(--ink-soft); font-family: var(--font-mono); }
  .audit-evidence-viewer pre { max-height: 280px; margin: 0; overflow: auto; padding: 10px; border-radius: 8px; color: var(--ink); background: var(--surface); font: var(--text-xs)/1.55 var(--font-mono); white-space: pre; }
  .audit-evidence-viewer a { justify-self: start; color: var(--purple); font-size: var(--text-xs); }
</style>
