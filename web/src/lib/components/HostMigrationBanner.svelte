<script lang="ts">
  import { clearHelmMigrationStorage } from '../hostMigration';

  export let canonicalOrigin = '';
  export let safeUrl = '';
  export let hasUnsavedDrafts = false;
  export let onNavigate: (() => void) | undefined;

  let confirmOpen = false;
  let clearing = false;
  let cleared = false;
  let clearError = '';

  function navigate(event: MouseEvent): void {
    event.preventDefault();
    if (onNavigate) {
      onNavigate();
    } else if (safeUrl && typeof window !== 'undefined') {
      window.location.assign(safeUrl);
    }
  }

  async function clearOldOriginData(): Promise<void> {
    if (clearing) return;
    clearing = true;
    clearError = '';
    try {
      const result = await clearHelmMigrationStorage();
      if (!result.offlineBoardsCleared || !result.staticCachesCleared) {
        clearError = 'Helm requested the old-address cleanup, but the browser could not confirm every Helm resource. You can try again.';
        return;
      }
      cleared = true;
      confirmOpen = false;
    } catch {
      clearError = 'The old-address Helm cache could not be cleared completely. You can try again.';
    } finally {
      clearing = false;
    }
  }
</script>

{#if canonicalOrigin && safeUrl}
  <aside class="host-migration-banner" role="alert" aria-labelledby="host-migration-heading" aria-describedby="host-migration-copy">
    <div class="host-migration-icon" aria-hidden="true">↗</div>
    <div class="host-migration-content">
      <h2 id="host-migration-heading">This Helm workspace moved</h2>
      <p id="host-migration-copy">
        You are viewing the old address. Changes are disabled here so this saved app cannot send writes to the new address. Any offline board remains read-only.
      </p>
      <p>
        Open <code>{canonicalOrigin}</code>, sign in again, and add the new address to your Home Screen again if you use Helm as an app. Browser sessions and cached data do not transfer automatically.
      </p>
      {#if hasUnsavedDrafts}
        <p class="host-migration-warning" role="status"><strong>Unsaved draft changes are still open.</strong> Copy them before leaving; writes are disabled here. Review and submit them at the new address.</p>
      {/if}
      {#if cleared}
        <p class="host-migration-success" role="status">Helm’s old-address offline boards and cached shell were cleared. Other site data was left untouched.</p>
      {/if}
      {#if clearError}
        <p class="host-migration-error" role="status">{clearError}</p>
      {/if}
      <div class="host-migration-actions">
        <a class="button primary" href={safeUrl} rel="noreferrer" on:click={navigate}>Open the new Helm address</a>
        <button class="button quiet-button" type="button" on:click={() => confirmOpen = true} disabled={clearing || cleared}>
          {cleared ? 'Old Helm cache cleared' : 'Clear old saved boards & cache'}
        </button>
      </div>
      {#if confirmOpen}
        <div class="host-migration-confirm" role="group" aria-label="Confirm old-address cleanup">
          <p>This clears only Helm offline board snapshots and Helm static service-worker caches on this old address. It does not clear other site data, browser sessions, drafts, or anything on the new address, and it cannot transfer them.</p>
          <div class="host-migration-confirm-actions">
            <button class="button primary" type="button" on:click={() => void clearOldOriginData()} disabled={clearing}>{clearing ? 'Clearing…' : 'Clear Helm data here'}</button>
            <button class="button quiet-button" type="button" on:click={() => confirmOpen = false} disabled={clearing}>Keep it</button>
          </div>
        </div>
      {/if}
    </div>
  </aside>
{/if}

<style>
  .host-migration-banner {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    margin: 14px 18px 0;
    padding: 14px 16px;
    border: 1px solid color-mix(in srgb, var(--amber), var(--border) 62%);
    border-radius: 10px;
    color: var(--ink-soft);
    background: var(--amber-soft);
    box-shadow: var(--shadow-sm);
  }

  .host-migration-icon {
    width: 26px;
    height: 26px;
    display: grid;
    place-items: center;
    flex: 0 0 auto;
    border-radius: 50%;
    color: #fff;
    background: var(--amber);
    font-size: 15px;
    font-weight: 800;
  }

  .host-migration-content { min-width: 0; }
  .host-migration-content h2 { margin: 0 0 5px; color: var(--ink); font: 700 15px/1.25 var(--font-display); }
  .host-migration-content p { max-width: 850px; margin: 5px 0 0; font-size: 12px; line-height: 1.5; }
  .host-migration-content code { overflow-wrap: anywhere; font-family: var(--font-mono, ui-monospace, monospace); font-size: .95em; }
  .host-migration-warning { color: var(--red); }
  .host-migration-success { color: var(--green); }
  .host-migration-error { color: var(--red); }
  .host-migration-actions, .host-migration-confirm-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
  .host-migration-actions .button, .host-migration-confirm-actions .button { min-height: 40px; }
  .host-migration-confirm { margin-top: 12px; padding-top: 12px; border-top: 1px solid color-mix(in srgb, var(--amber), var(--border) 64%); }

  @media (max-width: 600px) {
    .host-migration-banner { margin: 10px 10px 0; padding: 13px; }
    .host-migration-actions, .host-migration-confirm-actions { align-items: stretch; flex-direction: column; }
    .host-migration-actions .button, .host-migration-confirm-actions .button { width: 100%; min-height: 44px; justify-content: center; }
  }
</style>
