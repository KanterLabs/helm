<script context="module" lang="ts">
  import type { PwaUpdateGuard } from '../pwa';

  export type { PwaUpdateGuard };
</script>

<script lang="ts">
  import { onMount } from 'svelte';
  import {
    applyPwaUpdate,
    deferPwaUpdate,
    getPwaState,
    subscribePwa,
    type PwaState
  } from '../pwa';

  /** Final host-app guard for drafts and pending writes. False cancels activation. */
  export let beforeUpdate: PwaUpdateGuard | undefined = undefined;
  /** Pending host writes defer activation; rechecked by the final host guard. */
  export let updateBlocked = false;
  export let updateBlockedReason = 'Wait for the current save to finish before updating.';
  /** Set false when a host layout supplies its own install education. */
  export let showInstallInstructions = true;
  /** Set false when the host wants only update/error notices. */
  export let showCacheStatus = true;

  let pwa: PwaState = getPwaState();
  let isIos = false;
  let isStandalone = false;
  let showInstall = false;
  let installDismissed = false;
  let updateChecking = false;

  $: showUpdate = pwa.updateAvailable && !pwa.updateDeferred;

  const installDismissKey = 'helm:pwa-install-dismissed:v1';

  onMount(() => {
    const unsubscribe = subscribePwa((next) => {
      pwa = next;
    });

    isIos = detectIos();
    isStandalone = detectStandalone();
    installDismissed = readInstallDismissed();
    showInstall = showInstallInstructions && isIos && !isStandalone && !installDismissed;

    return unsubscribe;
  });

  function detectIos(): boolean {
    if (typeof navigator === 'undefined') return false;
    const userAgent = navigator.userAgent || '';
    const platform = navigator.platform || '';
    return /iPad|iPhone|iPod/i.test(userAgent)
      || (platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  }

  function detectStandalone(): boolean {
    if (typeof window === 'undefined') return false;
    const safariStandalone = Boolean((navigator as Navigator & { standalone?: boolean }).standalone);
    return safariStandalone || window.matchMedia?.('(display-mode: standalone)').matches === true;
  }

  function readInstallDismissed(): boolean {
    if (typeof window === 'undefined') return false;
    try {
      return window.localStorage.getItem(installDismissKey) === '1';
    } catch {
      return false;
    }
  }

  function dismissInstall(): void {
    showInstall = false;
    try {
      window.localStorage.setItem(installDismissKey, '1');
    } catch {
      // Private browsing can make localStorage unavailable; the dismissal is
      // still useful for this render even when it cannot be persisted.
    }
  }

  async function updateApp(): Promise<void> {
    if (updateBlocked || updateChecking || pwa.updateApplying) return;
    updateChecking = true;
    try {
      await applyPwaUpdate(async () => {
        if (updateBlocked) return false;
        const accepted = beforeUpdate
          ? await beforeUpdate()
          : window.confirm('Reload to update Helm? Unsaved changes may be lost.');
        // A save may have started while confirmation was open.
        return accepted && !updateBlocked;
      });
    } finally {
      updateChecking = false;
    }
  }

  function later(): void {
    deferPwaUpdate();
  }
</script>

{#if showInstall || ((showCacheStatus && (pwa.cacheReady && isStandalone || !pwa.online)) || Boolean(pwa.cacheError) || Boolean(pwa.error)) || showUpdate}
  <aside class="pwa-status-host" aria-label="Helm app status">
    <div class="pwa-status-stack">
      {#if showInstall}
        <section class="pwa-card install-card" aria-labelledby="pwa-install-title">
          <div class="pwa-card-icon" aria-hidden="true">⌂</div>
          <div class="pwa-card-copy">
            <strong id="pwa-install-title">Keep Helm handy</strong>
            <p>In Safari, tap Share, then <b>Add to Home Screen</b> to install Helm as an app.</p>
          </div>
          <button class="pwa-close" type="button" aria-label="Dismiss install instructions" on:click={dismissInstall}>×</button>
        </section>
      {/if}

      {#if showUpdate}
        <section class="pwa-card update-card" aria-live="polite" aria-labelledby="pwa-update-title">
          <div class="pwa-card-icon update-icon" aria-hidden="true">↻</div>
          <div class="pwa-card-copy">
            <strong id="pwa-update-title">A Helm update is ready</strong>
            <p>{updateBlocked ? updateBlockedReason : pwa.updateError || 'Reload when your current work is saved to use the latest version.'}</p>
          </div>
          <div class="pwa-update-actions">
            <button class="pwa-action pwa-later" type="button" disabled={pwa.updateApplying || updateChecking} on:click={later}>Later</button>
            <button class="pwa-action" type="button" disabled={updateBlocked || pwa.updateApplying || updateChecking} on:click={updateApp}>
              {pwa.updateApplying ? 'Updating…' : pwa.updateError ? 'Retry' : 'Reload'}
            </button>
          </div>
        </section>
      {/if}

      {#if ((showCacheStatus && (pwa.cacheReady && isStandalone || !pwa.online)) || pwa.cacheError || pwa.error)}
        <section class:offline={!pwa.online} class:error={Boolean(pwa.cacheError || pwa.error)} class="pwa-cache-status" role="status">
          <span class="pwa-status-dot" aria-hidden="true"></span>
          <span>
            {#if pwa.cacheError || pwa.error}
              <strong>Offline shell unavailable</strong>
              <small>{pwa.cacheError || pwa.error}</small>
            {:else if !pwa.online}
              <strong>Offline · read-only viewing</strong>
              <small>Saved snapshots remain available when cached.</small>
            {:else if pwa.appReady}
              <strong>Offline shell ready</strong>
              <small>Helm can open its cached shell without a network.</small>
            {:else}
              <strong>Preparing offline shell…</strong>
            {/if}
          </span>
        </section>
      {/if}
    </div>
  </aside>
{/if}

<style>
  .pwa-status-host {
    /* Participate in layout so save controls and navigation stay clear. */
    position: relative;
    display: flex;
    justify-content: flex-end;
    flex: 0 0 auto;
    padding: 8px max(12px, env(safe-area-inset-right)) 8px max(12px, env(safe-area-inset-left));
  }

  .pwa-status-stack {
    display: grid;
    gap: 8px;
    width: 100%;
  }

  .pwa-card,
  .pwa-cache-status {
    pointer-events: auto;
    border: 1px solid var(--border, #e5e8ef);
    border-radius: 12px;
    color: var(--ink, #1d2433);
    background: var(--surface-raised, #fff);
    box-shadow: var(--shadow-md, 0 14px 40px rgba(29, 36, 51, .12));
  }

  .pwa-card {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 12px;
  }

  .pwa-card-icon {
    width: 28px;
    height: 28px;
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    border-radius: 8px;
    color: #fff;
    background: var(--purple, #6d5efc);
    font-size: 16px;
    font-weight: 800;
  }

  .update-icon { background: var(--green, #2ea879); }

  .pwa-card-copy {
    min-width: 0;
    flex: 1;
  }

  .pwa-card-copy strong,
  .pwa-cache-status strong {
    display: block;
    font-size: 12px;
    font-weight: 800;
    line-height: 1.3;
  }

  .pwa-card-copy p,
  .pwa-cache-status small {
    display: block;
    margin: 4px 0 0;
    color: var(--muted, #596579);
    font-size: 11px;
    line-height: 1.45;
  }

  .pwa-close {
    width: 28px;
    height: 28px;
    flex: 0 0 auto;
    border: 0;
    border-radius: 7px;
    color: var(--muted, #596579);
    background: transparent;
    font-size: 18px;
    line-height: 1;
  }

  .pwa-close:hover { color: var(--ink, #1d2433); background: var(--surface-muted, #f0f2f7); }

  .pwa-action {
    min-height: 44px;
    align-self: center;
    flex: 0 0 auto;
    padding: 0 10px;
    border: 1px solid color-mix(in srgb, var(--purple, #6d5efc), var(--border, #e5e8ef) 40%);
    border-radius: 7px;
    color: var(--purple, #6d5efc);
    background: var(--purple-soft, #efedff);
    font-size: 11px;
    font-weight: 800;
  }

  .pwa-update-actions { display: flex; align-self: center; gap: 6px; flex: 0 0 auto; }
  .pwa-later { color: var(--muted, #596579); background: transparent; border-color: var(--border, #e5e8ef); }
  .pwa-action:disabled { opacity: .55; cursor: default; }

  .pwa-action:hover:not(:disabled) { background: color-mix(in srgb, var(--purple-soft, #efedff) 78%, var(--surface, #fff)); }

  .pwa-cache-status {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 9px 11px;
  }

  .pwa-status-dot {
    width: 8px;
    height: 8px;
    flex: 0 0 auto;
    margin-top: 4px;
    border-radius: 50%;
    background: var(--green, #2ea879);
    box-shadow: 0 0 0 3px var(--green-soft, #e7f7f0);
  }

  .pwa-cache-status.offline .pwa-status-dot { background: var(--amber, #d49534); box-shadow: 0 0 0 3px var(--amber-soft, #fff7e8); }
  .pwa-cache-status.error .pwa-status-dot { background: var(--red, #dc626f); box-shadow: 0 0 0 3px var(--red-soft, #fff0f1); }
  .pwa-cache-status.error small { color: var(--red, #dc626f); }

  @media (max-width: 600px) {
    .pwa-card { flex-wrap: wrap; }
    .pwa-update-actions { margin-left: 38px; }
    .pwa-card-copy { flex-basis: calc(100% - 38px); }
  }

  @media (prefers-reduced-motion: reduce) {
    .pwa-card, .pwa-cache-status { transition: none; }
  }
</style>
