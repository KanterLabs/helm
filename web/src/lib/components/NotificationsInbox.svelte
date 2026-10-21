<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../api';
  import { ApiError, type Notification, type NotificationPreferences, type Project, type Watch } from '../types';
  import NotificationItem from './NotificationItem.svelte';
  import NotificationPreferencesPanel from './NotificationPreferencesPanel.svelte';
  import NotificationWatchSection from './NotificationWatchSection.svelte';
  import { friendlyError, notificationLabel, unreadLabel, type PreferenceKey } from './notificationsInbox';

  export let pollIntervalMs = 60 * 1000;
  /** Changes whenever the authenticated actor/session changes. */
  export let sessionKey = '';
  export let activeProject: Project | undefined = undefined;
  export let onOpenNotification: (notification: Notification) => void | Promise<void> = () => undefined;

  const pageSize = 25;

  let open = false;
  let loading = false;
  let loadingOlder = false;
  let error = '';
  let notifications: Notification[] = [];
  let nextCursor: string | null = null;
  let disposed = false;
  let mounted = false;
  let sessionInvalidated = false;

  let listRequest = 0;
  let mutationRequest = 0;
  let watchRequest = 0;
  let watchMutationRequest = 0;
  let preferenceRequest = 0;
  let savingNotificationId = '';
  let savingAll = false;
  let watchSaving = false;
  let preferenceSavingKey: PreferenceKey | '' = '';

  let preferencesOpen = false;
  let preferencesLoading = false;
  let preferencesError = '';
  let preferences: NotificationPreferences | null = null;

  let watchesLoading = false;
  let watchesError = '';
  let projectWatch: Watch | null = null;

  let observedSessionKey = sessionKey;
  let observedWatchContextKey = '';

  $: watchContextKey = `${sessionKey}:${activeProject?.id || ''}`;
  $: saving = Boolean(savingNotificationId || savingAll || watchSaving || preferenceSavingKey);
  $: unreadCount = notifications.filter((notification) => !notification.read_at).length;

  $: if (mounted && sessionKey !== observedSessionKey) {
    observedSessionKey = sessionKey;
    clearSessionState(false);
    void loadNotifications(true);
  }

  $: if (mounted && watchContextKey !== observedWatchContextKey) {
    observedWatchContextKey = watchContextKey;
    resetWatchState();
    void loadWatches();
  }

  onMount(() => {
    mounted = true;
    disposed = false;
    observedSessionKey = sessionKey;
    observedWatchContextKey = watchContextKey;

    const refresh = () => {
      if (disposed || loading || loadingOlder || saving || sessionInvalidated) return;
      void loadNotifications(true);
    };
    const authInvalidated = () => clearSessionState(true);
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && open) {
        open = false;
        preferencesOpen = false;
      }
    };

    void loadNotifications(true);
    void loadWatches();
    const timer = window.setInterval(refresh, pollIntervalMs);
    window.addEventListener('helm:auth-invalidated', authInvalidated);
    window.addEventListener('keydown', keydown);

    return () => {
      disposed = true;
      mounted = false;
      window.clearInterval(timer);
      window.removeEventListener('helm:auth-invalidated', authInvalidated);
      window.removeEventListener('keydown', keydown);
    };
  });

  function isLiveSession(expectedSession: string): boolean {
    return mounted && !disposed && !sessionInvalidated && expectedSession === sessionKey;
  }

  function invalidateListReads(): void {
    listRequest += 1;
    loading = false;
    loadingOlder = false;
  }

  /** Drops all session-scoped state; an invalidated session stays unavailable until the key changes. */
  function clearSessionState(invalidated: boolean): void {
    listRequest += 1;
    mutationRequest += 1;
    preferenceRequest += 1;
    watchRequest += 1;
    watchMutationRequest += 1;
    sessionInvalidated = invalidated;
    notifications = [];
    nextCursor = null;
    loading = false;
    loadingOlder = false;
    error = invalidated ? 'Notifications are unavailable until your session is renewed.' : '';
    savingNotificationId = '';
    savingAll = false;
    preferenceSavingKey = '';
    preferences = null;
    preferencesOpen = false;
    preferencesLoading = false;
    preferencesError = '';
    resetWatchState();
  }

  async function loadNotifications(reset = true): Promise<void> {
    const expectedSession = sessionKey;
    if (!isLiveSession(expectedSession) || loading || loadingOlder || saving) return;
    const cursor = reset ? undefined : nextCursor || undefined;
    if (!reset && !cursor) return;

    const requestId = ++listRequest;
    if (reset) loading = true;
    else loadingOlder = true;

    try {
      const page = await api.listNotifications({ limit: pageSize, cursor });
      if (!isLiveSession(expectedSession) || requestId !== listRequest) return;
      const prior = reset ? [] : notifications;
      const seen = new Set(prior.map((notification) => notification.id));
      notifications = [...prior, ...page.data.filter((notification) => !seen.has(notification.id))];
      nextCursor = page.next_cursor || null;
      error = '';
    } catch (reason) {
      if (!isLiveSession(expectedSession) || requestId !== listRequest) return;
      if (reason instanceof ApiError && reason.status === 403) {
        notifications = [];
        nextCursor = null;
        error = 'Notifications are unavailable for this account.';
      } else {
        error = friendlyError(reason, 'Notifications could not be loaded.');
      }
    } finally {
      if (isLiveSession(expectedSession) && requestId === listRequest) {
        if (reset) loading = false;
        else loadingOlder = false;
      }
    }
  }

  function toggleInbox(): void {
    open = !open;
    if (!open) preferencesOpen = false;
    if (open && !loading && !sessionInvalidated) void loadNotifications(true);
  }

  async function activateNotification(notification: Notification): Promise<void> {
    const expectedSession = sessionKey;
    if (!isLiveSession(expectedSession) || saving) return;
    if (!notification.read_at) await setNotificationRead(notification, true);
    if (isLiveSession(expectedSession)) await onOpenNotification(notification);
  }

  async function setNotificationRead(notification: Notification, read: boolean): Promise<void> {
    const expectedSession = sessionKey;
    if (!isLiveSession(expectedSession) || saving || Boolean(notification.read_at) === read) return;
    const requestId = ++mutationRequest;
    invalidateListReads();
    savingNotificationId = notification.id;
    try {
      const updated = await api.markNotificationRead(notification.id, read);
      if (!isLiveSession(expectedSession) || requestId !== mutationRequest) return;
      notifications = notifications.map((item) => item.id === notification.id ? { ...item, ...updated, read_at: updated.read_at } : item);
      error = '';
    } catch (reason) {
      if (!isLiveSession(expectedSession) || requestId !== mutationRequest) return;
      error = friendlyError(reason, `Notification could not be marked ${read ? 'read' : 'unread'}.`);
    } finally {
      if (isLiveSession(expectedSession) && requestId === mutationRequest) savingNotificationId = '';
    }
  }

  async function markAllRead(): Promise<void> {
    const expectedSession = sessionKey;
    if (!isLiveSession(expectedSession) || saving || !unreadCount) return;
    const requestId = ++mutationRequest;
    invalidateListReads();
    savingAll = true;
    try {
      await api.markAllNotificationsRead();
      if (!isLiveSession(expectedSession) || requestId !== mutationRequest) return;
      const readAt = new Date().toISOString();
      notifications = notifications.map((notification) => notification.read_at ? notification : { ...notification, read_at: readAt });
      error = '';
    } catch (reason) {
      if (!isLiveSession(expectedSession) || requestId !== mutationRequest) return;
      error = friendlyError(reason, 'Notifications could not be marked read.');
    } finally {
      if (isLiveSession(expectedSession) && requestId === mutationRequest) savingAll = false;
    }
  }

  function resetWatchState(): void {
    watchesLoading = false;
    watchesError = '';
    projectWatch = null;
    watchSaving = false;
  }

  async function loadWatches(): Promise<void> {
    const expectedSession = sessionKey;
    const expectedContext = watchContextKey;
    const projectId = activeProject?.id;
    const requestId = ++watchRequest;
    if (!isLiveSession(expectedSession) || !projectId) return;
    watchesLoading = true;
    try {
      const watches = await api.listWatches({ project: projectId });
      if (!isLiveSession(expectedSession) || requestId !== watchRequest || expectedContext !== watchContextKey) return;
      projectWatch = watches.data.find((watch) => !watch.task_id && watch.project_id === projectId) || null;
      watchesError = '';
    } catch (reason) {
      if (!isLiveSession(expectedSession) || requestId !== watchRequest || expectedContext !== watchContextKey) return;
      if (reason instanceof ApiError && reason.status === 403) {
        projectWatch = null;
        watchesError = 'Watching is unavailable for this account.';
      } else {
        watchesError = friendlyError(reason, 'Watch status could not be loaded.');
      }
    } finally {
      if (isLiveSession(expectedSession) && requestId === watchRequest && expectedContext === watchContextKey) watchesLoading = false;
    }
  }

  async function toggleWatch(): Promise<void> {
    const expectedSession = sessionKey;
    const expectedContext = watchContextKey;
    const projectId = activeProject?.id;
    const existing = projectWatch;
    if (!isLiveSession(expectedSession) || !projectId || saving) return;
    const requestId = ++watchMutationRequest;
    watchRequest += 1;
    watchesLoading = false;
    watchSaving = true;
    try {
      const created = existing
        ? null
        : await api.createWatch({ project_id: projectId });
      if (existing) await api.deleteWatch(existing.id);
      if (!isLiveSession(expectedSession) || requestId !== watchMutationRequest || expectedContext !== watchContextKey) return;
      projectWatch = created;
      watchesError = '';
    } catch (reason) {
      if (!isLiveSession(expectedSession) || requestId !== watchMutationRequest || expectedContext !== watchContextKey) return;
      watchesError = friendlyError(reason, 'This project watch could not be updated.');
    } finally {
      if (isLiveSession(expectedSession) && requestId === watchMutationRequest && expectedContext === watchContextKey) watchSaving = false;
    }
  }

  function togglePreferences(): void {
    preferencesOpen = !preferencesOpen;
    if (preferencesOpen && !preferences && !preferencesLoading && !sessionInvalidated) void loadPreferences();
  }

  async function loadPreferences(): Promise<void> {
    const expectedSession = sessionKey;
    if (!isLiveSession(expectedSession) || preferencesLoading) return;
    const requestId = ++preferenceRequest;
    preferencesLoading = true;
    try {
      const result = await api.getNotificationPreferences();
      if (!isLiveSession(expectedSession) || requestId !== preferenceRequest) return;
      preferences = result;
      preferencesError = '';
    } catch (reason) {
      if (!isLiveSession(expectedSession) || requestId !== preferenceRequest) return;
      if (reason instanceof ApiError && reason.status === 403) {
        preferences = null;
        preferencesError = 'Notification preferences are unavailable for this account.';
      } else {
        preferencesError = friendlyError(reason, 'Notification preferences could not be loaded.');
      }
    } finally {
      if (isLiveSession(expectedSession) && requestId === preferenceRequest) preferencesLoading = false;
    }
  }

  async function setPreference(key: PreferenceKey): Promise<void> {
    const expectedSession = sessionKey;
    if (!isLiveSession(expectedSession) || !preferences || saving) return;
    const requestId = ++preferenceRequest;
    preferenceSavingKey = key;
    try {
      const updated = await api.patchNotificationPreferences({ [key]: !preferences[key] });
      if (!isLiveSession(expectedSession) || requestId !== preferenceRequest) return;
      preferences = updated;
      preferencesError = '';
    } catch (reason) {
      if (!isLiveSession(expectedSession) || requestId !== preferenceRequest) return;
      preferencesError = friendlyError(reason, 'Notification preference could not be updated.');
    } finally {
      if (isLiveSession(expectedSession) && requestId === preferenceRequest) preferenceSavingKey = '';
    }
  }
</script>

<div class="notifications-inbox">
  <button class="icon-button notifications-trigger" type="button" aria-label={notificationLabel(unreadCount)} aria-expanded={open} aria-controls="notifications-panel" aria-haspopup="dialog" on:click={toggleInbox}>
    <span aria-hidden="true">♢</span>
    {#if unreadCount}<span class="notifications-badge" aria-hidden="true">{unreadCount > 99 ? '99+' : unreadCount}</span>{/if}
  </button>

  {#if open}
    <div id="notifications-panel" class="notifications-panel" role="dialog" aria-modal="false" aria-labelledby="notifications-heading">
      <header class="notifications-heading">
        <div>
          <h2 id="notifications-heading">Notifications</h2>
          <p>{unreadLabel(sessionInvalidated, loading, unreadCount, notifications.length, nextCursor, error)}</p>
        </div>
        <div class="notifications-heading-actions">
          {#if unreadCount}<button class="text-button" type="button" disabled={saving} on:click={() => void markAllRead()}>Mark all read</button>{/if}
          <button class="icon-button tiny" type="button" aria-label="Close notifications" on:click={() => { open = false; preferencesOpen = false; }}>×</button>
        </div>
      </header>

      {#if error}
        <div class="notifications-alert" role="alert">{error}{#if !sessionInvalidated}<button class="text-button" type="button" on:click={() => void loadNotifications(true)}>Retry</button>{/if}</div>
      {/if}

      {#if loading && !notifications.length}
        <div class="notifications-status" role="status" aria-live="polite">Loading notifications…</div>
      {:else if !notifications.length && !error}
        <div class="notifications-empty">No notifications yet.</div>
      {:else if notifications.length}
        <div class="notifications-list" aria-busy={loading || loadingOlder}>
          {#each notifications as notification (notification.id)}
            <NotificationItem {notification} {saving} onOpen={activateNotification} onToggleRead={setNotificationRead} />
          {/each}
        </div>
        {#if nextCursor}
          <button class="notifications-more" type="button" disabled={loadingOlder || saving} on:click={() => void loadNotifications(false)}>{loadingOlder ? 'Loading…' : 'Load older notifications'}</button>
        {/if}
      {/if}

      {#if activeProject}
        <NotificationWatchSection watched={Boolean(projectWatch)} loading={watchesLoading} error={watchesError} {saving} onToggle={toggleWatch} onRetry={loadWatches} />
      {/if}

      <NotificationPreferencesPanel open={preferencesOpen} loading={preferencesLoading} {preferences} error={preferencesError} {saving} onToggle={togglePreferences} onSetPreference={setPreference} onRetry={loadPreferences} />
    </div>
  {/if}
</div>

<style>
  .notifications-inbox { position: relative; }
  .notifications-trigger { position: relative; }
  .notifications-trigger > span:first-child { font-size: var(--text-md); line-height: 1; }
  .notifications-badge { position: absolute; top: -3px; right: -3px; min-width: 1rem; padding: 0 .2rem; border: 2px solid var(--surface, #fff); border-radius: 999px; background: var(--danger, #d85555); color: #fff; font-size: var(--text-2xs); font-weight: 600; line-height: 1rem; text-align: center; }
  .notifications-panel { position: absolute; z-index: 20; top: calc(100% + .55rem); right: 0; display: grid; width: min(25rem, calc(100vw - 1.5rem)); max-height: min(38rem, calc(100vh - 5rem)); overflow: auto; border: 1px solid var(--border, #dedbe8); border-radius: .8rem; background: var(--surface, #fff); box-shadow: 0 .8rem 2.5rem rgba(25, 22, 42, .18); color: var(--text, #28233b); }
  .notifications-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: .75rem; padding: .85rem 1rem .7rem; border-bottom: 1px solid var(--border, #dedbe8); }
  .notifications-heading h2 { margin: 0; font-size: var(--text-md); }
  .notifications-heading p { margin: .2rem 0 0; color: var(--muted, #706b7f); font-size: var(--text-xs); }
  .notifications-heading-actions { display: flex; align-items: center; gap: .45rem; }
  .notifications-panel :global(.text-button) { padding: .2rem; color: var(--accent, #6558d8); font-size: var(--text-xs); }
  /* Shared with NotificationPreferencesPanel, so these reach into child components. */
  .notifications-panel :global(.notifications-alert), .notifications-panel :global(.notifications-status), .notifications-empty { padding: .75rem 1rem; color: var(--muted, #706b7f); font-size: var(--text-xs); }
  .notifications-panel :global(.notifications-alert) { display: flex; align-items: center; gap: .5rem; color: var(--danger, #b33d4b); }
  .notifications-panel :global(.notifications-alert .text-button) { margin-left: auto; }
  .notifications-list { border-bottom: 1px solid var(--border, #dedbe8); }
  .notifications-more { width: 100%; padding: .55rem; border: 0; border-bottom: 1px solid var(--border, #dedbe8); background: transparent; color: var(--accent, #6558d8); font-size: var(--text-xs); }
  @media (max-width: 600px) {
    .notifications-panel { position: fixed; top: 4.1rem; right: .75rem; left: .75rem; width: auto; max-height: calc(100vh - 5rem); }
  }
</style>
