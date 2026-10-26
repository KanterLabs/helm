<script lang="ts">
  // Project watch toggle shown at the bottom of the notifications panel.
  export let watched = false;
  export let loading = false;
  export let error = '';
  export let saving = false;
  export let onToggle: () => void | Promise<void> = () => undefined;
  export let onRetry: () => void | Promise<void> = () => undefined;
</script>

<div class="notifications-watch" aria-label="Notification watches">
  <div class="notifications-watch-heading"><strong>Watch</strong>{#if loading}<span>Updating…</span>{/if}</div>
  {#if error}<div class="notifications-watch-error" role="alert">{error}<button class="text-button" type="button" on:click={() => void onRetry()}>Retry</button></div>{/if}
  <div class="notifications-watch-actions">
    <button class="watch-toggle" type="button" aria-pressed={watched} disabled={saving || loading} on:click={() => void onToggle()}><span aria-hidden="true">◉</span>{watched ? 'Unwatch project' : 'Watch project'}</button>
  </div>
</div>

<style>
  .notifications-watch { padding: .7rem .85rem; border-bottom: 1px solid var(--border, #dedbe8); }
  .notifications-watch-heading { display: flex; justify-content: space-between; margin-bottom: .4rem; font-size: var(--text-xs); }
  .notifications-watch-heading span { color: var(--muted, #706b7f); font-size: var(--text-2xs); }
  .notifications-watch-error { display: flex; gap: .35rem; align-items: center; margin-bottom: .35rem; color: var(--danger, #b33d4b); font-size: var(--text-xs); }
  .notifications-watch-error .text-button { margin-left: auto; }
  .notifications-watch-actions { display: flex; flex-wrap: wrap; gap: .35rem; }
  .watch-toggle { padding: .3rem .5rem; border: 1px solid var(--border, #dedbe8); border-radius: .35rem; background: transparent; color: var(--text, #28233b); font-size: var(--text-xs); }
  .watch-toggle[aria-pressed="true"] { border-color: color-mix(in srgb, var(--accent, #6558d8) 55%, var(--border, #dedbe8)); background: color-mix(in srgb, var(--accent, #6558d8) 10%, transparent); color: var(--accent, #6558d8); }
</style>
