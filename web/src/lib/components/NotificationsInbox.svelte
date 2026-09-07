<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../api';
  import { ApiError, type Notification, type NotificationPreferences, type Project, type Watch } from '../types';

  type PreferenceKey = 'assignments' | 'mentions' | 'blockers' | 'state_changes';

  export let pollIntervalMs = 60 * 1000;
  /** Changes whenever the authenticated actor/session changes. */
  export let sessionKey = '';
  export let activeProject: Project | undefined = undefined;
  export let onOpenNotification: (notification: Notification) => void | Promise<void> = () => undefined;

  const pageSize = 25;
  const preferenceOptions: Array<{ key: PreferenceKey; label: string }> = [
    { key: 'assignments', label: 'Assignments' },
    { key: 'mentions', label: 'Mentions' },
    { key: 'blockers', label: 'Blockers' },
    { key: 'state_changes', label: 'State changes' }
  ];

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
    resetSessionState();
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
    const authInvalidated = () => invalidateSession();
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

  function resetSessionState(): void {
    listRequest += 1;
    mutationRequest += 1;
    preferenceRequest += 1;
    watchRequest += 1;
    watchMutationRequest += 1;
    sessionInvalidated = false;
    notifications = [];
    nextCursor = null;
    loading = false;
    loadingOlder = false;
    error = '';
    savingNotificationId = '';
    savingAll = false;
    preferenceSavingKey = '';
    preferences = null;
    preferencesOpen = false;
    preferencesLoading = false;
    preferencesError = '';
    resetWatchState();
  }

  function invalidateSession(): void {
    listRequest += 1;
    mutationRequest += 1;
    preferenceRequest += 1;
    watchRequest += 1;
    watchMutationRequest += 1;
    sessionInvalidated = true;
    notifications = [];
    nextCursor = null;
    loading = false;
    loadingOlder = false;
    error = 'Notifications are unavailable until your session is renewed.';
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

  function friendlyError(reason: unknown, fallback: string): string {
    return reason instanceof ApiError && reason.message ? reason.message : reason instanceof Error && reason.message ? reason.message : fallback;
  }

  function notificationTime(notification: Notification): string {
    const parsed = Date.parse(notification.created_at);
    if (Number.isNaN(parsed)) return 'Unknown time';
    const minutes = Math.round((parsed - Date.now()) / 60000);
    const absolute = Math.abs(minutes);
    if (absolute < 1) return 'just now';
    if (absolute < 60) return `${absolute}m ${minutes < 0 ? 'ago' : 'from now'}`;
    const hours = Math.round(absolute / 60);
    if (hours < 24) return `${hours}h ${minutes < 0 ? 'ago' : 'from now'}`;
    return `${Math.round(hours / 24)}d ${minutes < 0 ? 'ago' : 'from now'}`;
  }

  function notificationLabel(count: number): string {
    return count ? `Open notifications, ${count} unread` : 'Open notifications';
  }

  function unreadLabel(invalidated: boolean, busy: boolean, count: number, total: number, cursor: string | null, failure: string): string {
    if (invalidated) return 'Session expired';
    if (busy && !total) return 'Loading…';
    if (failure) return 'Inbox needs attention';
    if (!count) return cursor ? 'No unread on this page' : 'All caught up';
    return `${count} unread on this page`;
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
            <article class:unread={!notification.read_at} class="notification-item">
              <button class="notification-open" type="button" data-notification-open={notification.id} disabled={saving} aria-label={`Open notification: ${notification.title}`} on:click={() => void activateNotification(notification)}>
                <span class="notification-dot" aria-hidden="true"></span>
                <span class="notification-copy"><strong>{notification.title}</strong><span>{notification.body}</span><time datetime={notification.created_at}>{notificationTime(notification)}</time></span>
              </button>
              <button class="notification-state-button" type="button" data-notification-read-toggle={notification.id} disabled={saving} aria-label={notification.read_at ? `Mark ${notification.title} unread` : `Mark ${notification.title} read`} on:click={() => void setNotificationRead(notification, !notification.read_at)}>{notification.read_at ? 'Unread' : 'Read'}</button>
            </article>
          {/each}
        </div>
        {#if nextCursor}
          <button class="notifications-more" type="button" disabled={loadingOlder || saving} on:click={() => void loadNotifications(false)}>{loadingOlder ? 'Loading…' : 'Load older notifications'}</button>
        {/if}
      {/if}

      {#if activeProject}
        <div class="notifications-watch" aria-label="Notification watches">
          <div class="notifications-watch-heading"><strong>Watch</strong>{#if watchesLoading}<span>Updating…</span>{/if}</div>
          {#if watchesError}<div class="notifications-watch-error" role="alert">{watchesError}<button class="text-button" type="button" on:click={() => void loadWatches()}>Retry</button></div>{/if}
          <div class="notifications-watch-actions">
            <button class="watch-toggle" type="button" aria-pressed={Boolean(projectWatch)} disabled={saving || watchesLoading} on:click={() => void toggleWatch()}><span aria-hidden="true">◉</span>{projectWatch ? 'Unwatch project' : 'Watch project'}</button>
          </div>
        </div>
      {/if}

      <div class="notifications-preferences">
        <button class="notifications-preferences-trigger" type="button" aria-expanded={preferencesOpen} aria-controls="notifications-preferences-panel" on:click={togglePreferences}>Notification preferences <span aria-hidden="true">{preferencesOpen ? '⌃' : '⌄'}</span></button>
        {#if preferencesOpen}
          <div id="notifications-preferences-panel" class="notifications-preferences-panel">
            {#if preferencesLoading}<div class="notifications-status" role="status">Loading preferences…</div>{:else if preferences}
              <fieldset disabled={saving}>
                <legend class="sr-only">Notification preferences</legend>
                {#each preferenceOptions as option}
                  <label><input type="checkbox" checked={preferences[option.key]} on:change={() => void setPreference(option.key)} /><span>{option.label}</span></label>
                {/each}
              </fieldset>
            {/if}
            {#if preferencesError}<div class="notifications-alert" role="alert">{preferencesError}<button class="text-button" type="button" on:click={() => void loadPreferences()}>Retry</button></div>{/if}
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .notifications-inbox { position: relative; }
  .notifications-trigger { position: relative; }
  .notifications-trigger > span:first-child { font-size: 1.05rem; line-height: 1; }
  .notifications-badge { position: absolute; top: -3px; right: -3px; min-width: 1rem; padding: 0 .2rem; border: 2px solid var(--surface, #fff); border-radius: 999px; background: var(--danger, #d85555); color: #fff; font-size: .58rem; font-weight: 700; line-height: 1rem; text-align: center; }
  .notifications-panel { position: absolute; z-index: 20; top: calc(100% + .55rem); right: 0; display: grid; width: min(25rem, calc(100vw - 1.5rem)); max-height: min(38rem, calc(100vh - 5rem)); overflow: auto; border: 1px solid var(--border, #dedbe8); border-radius: .8rem; background: var(--surface, #fff); box-shadow: 0 .8rem 2.5rem rgba(25, 22, 42, .18); color: var(--text, #28233b); }
  .notifications-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: .75rem; padding: .85rem 1rem .7rem; border-bottom: 1px solid var(--border, #dedbe8); }
  .notifications-heading h2 { margin: 0; font-size: .95rem; }
  .notifications-heading p { margin: .2rem 0 0; color: var(--muted, #706b7f); font-size: .72rem; }
  .notifications-heading-actions { display: flex; align-items: center; gap: .45rem; }
  .notifications-panel .text-button { padding: .2rem; color: var(--accent, #6558d8); font-size: .7rem; }
  .notifications-alert, .notifications-status, .notifications-empty { padding: .75rem 1rem; color: var(--muted, #706b7f); font-size: .75rem; }
  .notifications-alert { display: flex; align-items: center; gap: .5rem; color: var(--danger, #b33d4b); }
  .notifications-alert .text-button { margin-left: auto; }
  .notifications-list { border-bottom: 1px solid var(--border, #dedbe8); }
  .notification-item { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: .35rem; align-items: center; padding: .1rem .65rem .1rem .85rem; border-bottom: 1px solid color-mix(in srgb, var(--border, #dedbe8) 65%, transparent); }
  .notification-item:last-child { border-bottom: 0; }
  .notification-item.unread { background: color-mix(in srgb, var(--accent, #6558d8) 6%, transparent); }
  .notification-open { display: grid; grid-template-columns: .45rem minmax(0, 1fr); gap: .55rem; min-width: 0; padding: .58rem 0; border: 0; background: transparent; color: inherit; text-align: left; }
  .notification-open:disabled { opacity: .6; }
  .notification-dot { width: .42rem; height: .42rem; margin-top: .34rem; border-radius: 50%; background: transparent; }
  .notification-item.unread .notification-dot { background: var(--accent, #6558d8); }
  .notification-copy { display: grid; min-width: 0; gap: .12rem; }
  .notification-copy strong, .notification-copy > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .notification-copy strong { font-size: .75rem; }
  .notification-copy > span { color: var(--muted, #706b7f); font-size: .7rem; }
  .notification-copy time { color: var(--muted, #706b7f); font-size: .65rem; }
  .notification-state-button { padding: .2rem; border: 0; background: transparent; color: var(--muted, #706b7f); font-size: .65rem; }
  .notification-state-button:hover, .notification-state-button:focus-visible { color: var(--accent, #6558d8); }
  .notifications-more { width: 100%; padding: .55rem; border: 0; border-bottom: 1px solid var(--border, #dedbe8); background: transparent; color: var(--accent, #6558d8); font-size: .72rem; }
  .notifications-watch { padding: .7rem .85rem; border-bottom: 1px solid var(--border, #dedbe8); }
  .notifications-watch-heading { display: flex; justify-content: space-between; margin-bottom: .4rem; font-size: .72rem; }
  .notifications-watch-heading span { color: var(--muted, #706b7f); font-size: .65rem; }
  .notifications-watch-error { display: flex; gap: .35rem; align-items: center; margin-bottom: .35rem; color: var(--danger, #b33d4b); font-size: .68rem; }
  .notifications-watch-error .text-button { margin-left: auto; }
  .notifications-watch-actions { display: flex; flex-wrap: wrap; gap: .35rem; }
  .watch-toggle { padding: .3rem .5rem; border: 1px solid var(--border, #dedbe8); border-radius: .35rem; background: transparent; color: var(--text, #28233b); font-size: .68rem; }
  .watch-toggle[aria-pressed="true"] { border-color: color-mix(in srgb, var(--accent, #6558d8) 55%, var(--border, #dedbe8)); background: color-mix(in srgb, var(--accent, #6558d8) 10%, transparent); color: var(--accent, #6558d8); }
  .notifications-preferences-trigger { display: flex; justify-content: space-between; width: 100%; padding: .7rem .85rem; border: 0; background: transparent; color: inherit; font-size: .72rem; text-align: left; }
  .notifications-preferences-panel { padding: 0 .85rem .8rem; }
  .notifications-preferences-panel fieldset { display: grid; grid-template-columns: 1fr 1fr; gap: .45rem; margin: 0; padding: 0; border: 0; }
  .notifications-preferences-panel label { display: flex; align-items: center; gap: .35rem; color: var(--muted, #706b7f); font-size: .68rem; }
  .notifications-preferences-panel input { margin: 0; accent-color: var(--accent, #6558d8); }
  @media (max-width: 600px) {
    .notifications-panel { position: fixed; top: 4.1rem; right: .75rem; left: .75rem; width: auto; max-height: calc(100vh - 5rem); }
  }
</style>
