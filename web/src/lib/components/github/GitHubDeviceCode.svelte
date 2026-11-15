<script lang="ts">
  import { copyText } from '../../taskShare';
  import type { GithubConnectAttempt } from '../../types';
  import { githubUrl, timeLeft } from './connection';

  /** The pending attempt this administrator began (it carries `user_code`). */
  export let attempt: GithubConnectAttempt;
  export let now: number;
  export let busy = false;
  export let onCancel: () => void;
  /** Speaks a short status through the panel's live region. */
  export let announce: (text: string) => void;

  let codeEl: HTMLElement;
  let copyStatus = '';
  let copyFailed = false;
  let shownFor = '';

  // A new attempt never inherits the previous attempt's copy result.
  $: if (attempt.id !== shownFor) { shownFor = attempt.id; copyStatus = ''; copyFailed = false; }
  $: openUrl = githubUrl(attempt.verification_uri);
  $: remaining = timeLeft(attempt.expires_at, now);

  async function copy(): Promise<void> {
    const code = attempt.user_code || '';
    const id = attempt.id;
    const result = await copyText(code);
    if (id !== attempt.id) return;
    copyFailed = !result.ok;
    if (result.ok) {
      copyStatus = 'Copied. Paste it on GitHub.';
    } else {
      selectCode();
      copyStatus = result.reason === 'denied'
        ? 'Your browser blocked copying. The code is selected: press Ctrl+C (⌘C on a Mac) to copy it.'
        : 'This browser cannot copy from here. The code is selected: press Ctrl+C (⌘C on a Mac) to copy it.';
    }
    announce(copyStatus);
  }

  function selectCode(): void {
    if (!codeEl) return;
    const range = document.createRange();
    range.selectNodeContents(codeEl);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
  }
</script>

<div class="device-code" data-github-attempt={attempt.id}>
  <h3 tabindex="-1" data-github-view-heading>Enter this code on GitHub</h3>
  <p class="lead">Copy the code, open GitHub, paste it and approve Helm. This page updates by itself when you are done.</p>
  <div class="code-row">
    <span class="sr-only">One-time code:</span>
    <span class="code" data-github-user-code bind:this={codeEl}>{attempt.user_code}</span>
    <button class="button quiet-button copy" type="button" on:click={() => void copy()}>Copy code</button>
  </div>
  {#if copyStatus}<p class="copy-status" class:failed={copyFailed} data-github-copy-status>{copyStatus}</p>{/if}
  <div class="actions">
    {#if openUrl}
      <a class="button primary button-large" href={openUrl} target="_blank" rel="noopener noreferrer">Open GitHub <span aria-hidden="true">↗</span><span class="sr-only"> (opens in a new tab)</span></a>
    {:else}
      <p class="manual">On GitHub, go to <strong>github.com/login/device</strong> and enter this code.</p>
    {/if}
    <button class="text-button" type="button" disabled={busy} on:click={onCancel}>Cancel</button>
  </div>
  <p class="meta" data-github-time-left><span class="waiting" aria-hidden="true"></span><span>Waiting for approval · code expires in <strong>{remaining}</strong></span></p>
</div>

<style>
  .device-code { display: grid; gap: 12px; justify-items: start; }
  .device-code > * { max-width: 100%; }
  h3 { margin: 0; font-size: var(--text-base); outline: none; }
  .lead, .manual, .meta, .copy-status { margin: 0; color: var(--ink-soft); font-size: var(--text-sm); line-height: 1.5; }
  .code-row { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; }
  .code { padding: 10px 16px; border: 1px dashed color-mix(in srgb, var(--purple), var(--border) 45%); border-radius: 10px; color: var(--ink); background: var(--surface-muted); font: 600 var(--text-xl)/1.15 var(--font-mono); letter-spacing: .12em; user-select: all; -webkit-user-select: all; cursor: text; white-space: nowrap; }
  .copy { min-height: 44px; }
  .copy-status { color: var(--semantic-green); font-size: var(--text-xs); font-weight: 600; }
  .copy-status.failed { color: var(--semantic-amber); }
  .actions { display: flex; flex-wrap: wrap; align-items: center; gap: 10px 18px; }
  .actions .text-button { min-height: 44px; }
  .meta { display: flex; align-items: center; gap: 8px; color: var(--muted); font-size: var(--text-xs); }
  .meta strong { color: var(--ink-soft); font-variant-numeric: tabular-nums; }
  .waiting { width: 8px; height: 8px; flex: 0 0 8px; border-radius: 50%; background: var(--purple); animation: pulse 1.6s ease-in-out infinite; }
  @keyframes pulse { 50% { opacity: .3; } }
  @media (prefers-reduced-motion: reduce) { .waiting { animation: none; } }
</style>
