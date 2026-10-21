<script lang="ts">
  // Collapsible per-category notification preferences. State and requests stay in NotificationsInbox.
  import type { NotificationPreferences } from '../types';
  import { preferenceOptions, type PreferenceKey } from './notificationsInbox';

  export let open = false;
  export let loading = false;
  export let preferences: NotificationPreferences | null = null;
  export let error = '';
  export let saving = false;
  export let onToggle: () => void = () => undefined;
  export let onSetPreference: (key: PreferenceKey) => void | Promise<void> = () => undefined;
  export let onRetry: () => void | Promise<void> = () => undefined;
</script>

<div class="notifications-preferences">
  <button class="notifications-preferences-trigger" type="button" aria-expanded={open} aria-controls="notifications-preferences-panel" on:click={onToggle}>Notification preferences <span aria-hidden="true">{open ? '⌃' : '⌄'}</span></button>
  {#if open}
    <div id="notifications-preferences-panel" class="notifications-preferences-panel">
      {#if loading}<div class="notifications-status" role="status">Loading preferences…</div>{:else if preferences}
        <fieldset disabled={saving}>
          <legend class="sr-only">Notification preferences</legend>
          {#each preferenceOptions as option}
            <label><input type="checkbox" checked={preferences[option.key]} on:change={() => void onSetPreference(option.key)} /><span>{option.label}</span></label>
          {/each}
        </fieldset>
      {/if}
      {#if error}<div class="notifications-alert" role="alert">{error}<button class="text-button" type="button" on:click={() => void onRetry()}>Retry</button></div>{/if}
    </div>
  {/if}
</div>

<style>
  .notifications-preferences-trigger { display: flex; justify-content: space-between; width: 100%; padding: .7rem .85rem; border: 0; background: transparent; color: inherit; font-size: var(--text-xs); text-align: left; }
  .notifications-preferences-panel { padding: 0 .85rem .8rem; }
  .notifications-preferences-panel fieldset { display: grid; grid-template-columns: 1fr 1fr; gap: .45rem; margin: 0; padding: 0; border: 0; }
  .notifications-preferences-panel label { display: flex; align-items: center; gap: .35rem; color: var(--muted, #706b7f); font-size: var(--text-xs); }
  .notifications-preferences-panel input { margin: 0; accent-color: var(--accent, #6558d8); }
</style>
