// Reactive state: Event polling, liveness timers, announcements and toasts.
import type { ActivityEvent } from '../../lib/types';
import type { ToastItem } from '../types';

class LiveState {
  events = $state.raw<ActivityEvent[]>([]);
  eventsCursor = $state.raw<number | undefined>();
  pollTimer = $state.raw<number | undefined>();
  pulseTimer = $state.raw<number | undefined>();
  livenessRefreshTimer = $state.raw<number | undefined>();
  pollInFlight = $state.raw<Promise<void> | null>(null);
  boardLivenessRequest = $state.raw(0);
  myWorkLivenessRequest = $state.raw(0);
  drawerLivenessRequest = $state.raw(0);
  boardLivenessInFlight = $state.raw<Promise<boolean> | null>(null);
  boardBackgroundInFlight = $state.raw<Promise<boolean> | null>(null);
  myWorkLivenessInFlight = $state.raw<Promise<boolean> | null>(null);
  drawerLivenessInFlight = $state.raw<{ taskId: string; requestId: number; promise: Promise<boolean> } | null>(null);
  pulseClock = $state.raw(Date.now());
  liveAnnouncement = $state.raw('');
  announcementTimer = $state.raw<number | undefined>();
  workTransitionSnapshot = $state.raw(new Map<string, string>());
  toastSequence = $state.raw(0);
  toasts = $state.raw<ToastItem[]>([]);
}

export const live = new LiveState();
