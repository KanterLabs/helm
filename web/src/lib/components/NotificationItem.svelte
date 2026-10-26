<script lang="ts">
  // One inbox row: open the notification, or flip its read state.
  import type { Notification } from '../types';
  import { notificationTime } from './notificationsInbox';

  export let notification: Notification;
  export let saving = false;
  export let onOpen: (notification: Notification) => void | Promise<void> = () => undefined;
  export let onToggleRead: (notification: Notification, read: boolean) => void | Promise<void> = () => undefined;
</script>

<article class:unread={!notification.read_at} class="notification-item">
  <button class="notification-open" type="button" data-notification-open={notification.id} disabled={saving} aria-label={`Open notification: ${notification.title}`} on:click={() => void onOpen(notification)}>
    <span class="notification-dot" aria-hidden="true"></span>
    <span class="notification-copy"><strong>{notification.title}</strong><span>{notification.body}</span><time datetime={notification.created_at}>{notificationTime(notification)}</time></span>
  </button>
  <button class="notification-state-button" type="button" data-notification-read-toggle={notification.id} disabled={saving} aria-label={notification.read_at ? `Mark ${notification.title} unread` : `Mark ${notification.title} read`} on:click={() => void onToggleRead(notification, !notification.read_at)}>{notification.read_at ? 'Unread' : 'Read'}</button>
</article>

<style>
  .notification-item { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: .35rem; align-items: center; padding: .1rem .65rem .1rem .85rem; border-bottom: 1px solid color-mix(in srgb, var(--border, #dedbe8) 65%, transparent); }
  .notification-item:last-child { border-bottom: 0; }
  .notification-item.unread { background: color-mix(in srgb, var(--accent, #6558d8) 6%, transparent); }
  .notification-open { display: grid; grid-template-columns: .45rem minmax(0, 1fr); gap: .55rem; min-width: 0; padding: .58rem 0; border: 0; background: transparent; color: inherit; text-align: left; }
  .notification-open:disabled { opacity: .6; }
  .notification-dot { width: .42rem; height: .42rem; margin-top: .34rem; border-radius: 50%; background: transparent; }
  .notification-item.unread .notification-dot { background: var(--accent, #6558d8); }
  .notification-copy { display: grid; min-width: 0; gap: .12rem; }
  .notification-copy strong, .notification-copy > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .notification-copy strong { font-size: var(--text-xs); }
  .notification-copy > span { color: var(--muted, #706b7f); font-size: var(--text-xs); }
  .notification-copy time { color: var(--muted, #706b7f); font-size: var(--text-2xs); }
  .notification-state-button { padding: .2rem; border: 0; background: transparent; color: var(--muted, #706b7f); font-size: var(--text-2xs); }
  .notification-state-button:hover, .notification-state-button:focus-visible { color: var(--accent, #6558d8); }
</style>
