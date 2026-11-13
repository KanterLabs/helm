<script lang="ts">
  import { onDestroy } from 'svelte';

  /** One value the person enters on the forge, with a copy button beside it. */
  export let label: string;
  export let value: string;
  /** Lower-case noun for the button name and announcements ("redirect URI"). */
  export let noun: string;
  export let mono = false;
  export let attrs: Record<string, string> = {};
  export let announce: (text: string) => void = () => undefined;

  let node: HTMLElement;
  let copied = false;
  let failed = false;
  let reset: ReturnType<typeof setTimeout> | undefined;

  function selectValue() {
    const range = document.createRange();
    range.selectNodeContents(node);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
  }

  async function copy() {
    clearTimeout(reset);
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable');
      await navigator.clipboard.writeText(value);
      failed = false;
      copied = true;
      announce(`Copied ${noun}`);
      reset = setTimeout(() => { copied = false; }, 2000);
    } catch {
      copied = false;
      failed = true;
      selectValue();
      announce(`Copying did not work. The ${noun} is selected; copy it with your keyboard.`);
    }
  }

  onDestroy(() => clearTimeout(reset));
</script>

<div class="gitea-copy">
  <dt>{label}</dt>
  <dd>
    <code class:mono bind:this={node} {...attrs}>{value}</code>
    <button class="button quiet-button compact-button gitea-copy-button" type="button" aria-label={`Copy ${noun}`} on:click={copy}>{copied ? 'Copied' : 'Copy'}</button>
  </dd>
  {#if failed}<p class="gitea-copy-failed" data-gitea-copy-failed>Copying did not work here. The value is selected: press Ctrl+C (⌘C on a Mac).</p>{/if}
</div>

<style>
  .gitea-copy { display: grid; grid-template-columns: 132px minmax(0, 1fr); gap: 4px 12px; align-items: center; }
  dt { color: var(--muted); font-size: var(--text-xs); font-weight: 600; }
  dd { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px; align-items: center; margin: 0; }
  code { min-width: 0; padding: 7px 10px; border: 1px solid var(--border); border-radius: 8px; color: var(--ink); background: var(--surface-muted); font-family: var(--font-mono); font-size: var(--text-xs); line-height: 1.45; overflow-wrap: anywhere; word-break: break-word; user-select: all; }
  .gitea-copy-button { min-width: 64px; }
  .gitea-copy-failed { grid-column: 1 / -1; margin: 0; color: var(--semantic-amber); font-size: var(--text-xs); }
  @media (max-width: 640px) {
    .gitea-copy { grid-template-columns: minmax(0, 1fr); }
    .gitea-copy-button { min-height: 44px; }
  }
</style>
