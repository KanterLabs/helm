// Labels and formatting helpers for NotificationsInbox.svelte.
import { ApiError, type Notification } from '../types';

export type PreferenceKey = 'assignments' | 'mentions' | 'blockers' | 'state_changes';

export const preferenceOptions: Array<{ key: PreferenceKey; label: string }> = [
  { key: 'assignments', label: 'Assignments' },
  { key: 'mentions', label: 'Mentions' },
  { key: 'blockers', label: 'Blockers' },
  { key: 'state_changes', label: 'State changes' }
];

export function friendlyError(reason: unknown, fallback: string): string {
  return reason instanceof ApiError && reason.message ? reason.message : reason instanceof Error && reason.message ? reason.message : fallback;
}

export function notificationTime(notification: Notification): string {
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

export function notificationLabel(count: number): string {
  return count ? `Open notifications, ${count} unread` : 'Open notifications';
}

export function unreadLabel(invalidated: boolean, busy: boolean, count: number, total: number, cursor: string | null, failure: string): string {
  if (invalidated) return 'Session expired';
  if (busy && !total) return 'Loading…';
  if (failure) return 'Inbox needs attention';
  if (!count) return cursor ? 'No unread on this page' : 'All caught up';
  return `${count} unread on this page`;
}
