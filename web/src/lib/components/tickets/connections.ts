// Connect apps manager: tab model, status summaries and the dialog's
// keyboard containment. See docs/TC337_TICKETS_UX_FAILURES.md.
import type { PublicEndpointView } from '../../types';

export type ConnectionTab = 'email' | 'webhooks' | 'public';
export type ConnectionTone = 'live' | 'warn' | 'off' | 'count';

export const connectionTabs: { id: ConnectionTab; label: string }[] = [
  { id: 'email', label: 'Email' },
  { id: 'webhooks', label: 'Webhooks' },
  { id: 'public', label: 'Public access' }
];

/**
 * Where the manager opens: Public access when it needs attention (not set
 * up, not live, or returning from Cloudflare sign-in), otherwise Email.
 * Non-admins only ever see the read-only webhook guide.
 */
export function defaultConnectionTab(admin: boolean | undefined, view: PublicEndpointView | null, returningFromCloudflare = false): ConnectionTab {
  if (!admin) return 'webhooks';
  if (returningFromCloudflare) return 'public';
  if (view && (!view.active || view.connector.state !== 'connected')) return 'public';
  return 'email';
}

/** The short status the Public access tab shows; matches the panel badge. */
export function publicAccessStatus(view: PublicEndpointView | null): { label: string; tone: ConnectionTone } {
  if (!view) return { label: '', tone: 'off' };
  if (!view.active) return { label: 'Off', tone: 'off' };
  return view.connector.state === 'connected' ? { label: 'Live', tone: 'live' } : { label: 'Connecting', tone: 'warn' };
}

/** Arrow/Home/End movement across the tablist (wrapping). */
export function nextConnectionTab(current: ConnectionTab, key: string): ConnectionTab | null {
  const ids = connectionTabs.map((tab) => tab.id);
  const index = ids.indexOf(current);
  if (key === 'ArrowRight') return ids[(index + 1) % ids.length];
  if (key === 'ArrowLeft') return ids[(index - 1 + ids.length) % ids.length];
  if (key === 'Home') return ids[0];
  if (key === 'End') return ids[ids.length - 1];
  return null;
}

const focusableSelector = [
  'a[href]',
  'button:not(:disabled)',
  'input:not(:disabled):not([type="hidden"])',
  'select:not(:disabled)',
  'textarea:not(:disabled)',
  'summary',
  '[tabindex]:not([tabindex="-1"])'
].join(',');

function visible(element: HTMLElement): boolean {
  // Roving tabindex: unselected tabs are reachable by arrows, not Tab.
  if (element.tabIndex < 0 || element.closest('[hidden], [aria-hidden="true"]')) return false;
  if (element.closest('details:not([open]) > :not(summary)')) return false;
  const style = window.getComputedStyle(element);
  return style.display !== 'none' && style.visibility !== 'hidden' && element.getClientRects().length > 0;
}

/** Keeps Tab and Shift+Tab inside `scope`, resolving controls on each press. */
export function containTab(event: KeyboardEvent, scope: HTMLElement) {
  const elements = Array.from(scope.querySelectorAll<HTMLElement>(focusableSelector)).filter(visible);
  if (!elements.length) {
    event.preventDefault();
    scope.focus();
    return;
  }
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const index = active ? elements.indexOf(active) : -1;
  if (event.shiftKey && index <= 0) {
    event.preventDefault();
    elements[elements.length - 1].focus();
  } else if (!event.shiftKey && (index < 0 || index === elements.length - 1)) {
    event.preventDefault();
    elements[0].focus();
  }
}
