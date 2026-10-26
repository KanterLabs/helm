// Focus trapping, dialog focus restoration and confirmation prompts.
import { tick } from 'svelte';
import { dialogs } from './state/dialogs.svelte';
import type { ConfirmRequest } from './types';

export const focusableSelector = [
  'a[href]',
  'area[href]',
  'button:not(:disabled)',
  'input:not(:disabled):not([type="hidden"])',
  'select:not(:disabled)',
  'textarea:not(:disabled)',
  'audio[controls]',
  'video[controls]',
  '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])'
].join(',');

export function isFocusableVisible(element: HTMLElement): boolean {
  if (!element.isConnected || element.hasAttribute('hidden') || element.getAttribute('aria-hidden') === 'true' || element.matches(':disabled')) return false;
  if (typeof window === 'undefined') return true;
  const style = window.getComputedStyle(element);
  return style.display !== 'none' && style.visibility !== 'hidden';
}

export function focusableElements(node: HTMLElement): HTMLElement[] {
  return Array.from(node.querySelectorAll<HTMLElement>(focusableSelector)).filter(isFocusableVisible);
}

// A single action keeps keyboard focus inside every modal, while resolving
// the focusable list on each Tab keypress so async/dynamic controls are
// included without needing action updates or per-dialog handlers.
export function focusTrap(node: HTMLElement) {
  if (!node.hasAttribute('tabindex')) node.tabIndex = -1;

  const handleKeydown = (event: KeyboardEvent) => {
    if (dialogs.confirmRequest) return;
    if (event.key !== 'Tab') return;
    const focusable = focusableElements(node);
    if (!focusable.length) {
      event.preventDefault();
      node.focus();
      return;
    }
    const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const index = active ? focusable.indexOf(active) : -1;
    if (event.shiftKey) {
      if (index <= 0) {
        event.preventDefault();
        focusable[focusable.length - 1].focus();
      }
    } else if (index < 0 || index === focusable.length - 1) {
      event.preventDefault();
      focusable[0].focus();
    }
  };

  node.addEventListener('keydown', handleKeydown);
  void tick().then(() => {
    if (!node.isConnected || node.contains(document.activeElement)) return;
    const initial = node.querySelector<HTMLElement>('[data-dialog-initial-focus]');
    const focusable = focusableElements(node);
    // A dialog may deliberately place initial focus on a neutral, programmatic
    // target (tabindex=-1) instead of moving a user straight into a field.
    const target = initial && isFocusableVisible(initial) ? initial : focusable[0];
    (target || node).focus();
  });

  return { destroy: () => node.removeEventListener('keydown', handleKeydown) };
}

export function rememberDialogFocus(fallbackSelector = '') {
  dialogs.dialogFocusRestoreRequest += 1;
  if (typeof document !== 'undefined') {
    const active = document.activeElement instanceof HTMLElement && document.activeElement !== document.body
      ? document.activeElement
      : null;
    dialogs.dialogReturnFocus = {
      element: active && isFocusableVisible(active) ? active : null,
      fallbackSelector
    };
  }
}

export function restoreDialogFocus(afterRender: Promise<unknown> = Promise.resolve()) {
  const record = dialogs.dialogReturnFocus;
  dialogs.dialogReturnFocus = null;
  if (!record) return;
  const request = ++dialogs.dialogFocusRestoreRequest;
  const location = window.location.pathname + window.location.search;
  const initialFocus = document.activeElement;
  void afterRender.catch(() => undefined).then(() => tick()).then(() => {
    if (request !== dialogs.dialogFocusRestoreRequest || window.location.pathname + window.location.search !== location) return;
    // A user can focus another control while the origin is loading. A
    // detached dialog/trigger naturally falls back to body; keep an explicit
    // later choice rather than moving focus away from it.
    const active = document.activeElement;
    if (active && active !== document.body && active !== initialFocus && active !== record.element && active.isConnected) return;
    const target = record.element && record.element !== document.body && isFocusableVisible(record.element)
      ? record.element
      : record.fallbackSelector
        ? Array.from(document.querySelectorAll<HTMLElement>(record.fallbackSelector)).find(isFocusableVisible)
        : null;
    target?.focus();
  });
}

export function requestConfirm(input: Omit<ConfirmRequest, 'resolve'>): Promise<boolean> {
  rememberDialogFocus(input.fallbackSelector || '');
  return new Promise((resolve) => {
    dialogs.confirmRequest = { ...input, resolve };
  });
}

export function settleConfirm(confirmed: boolean) {
  const request = dialogs.confirmRequest;
  dialogs.confirmRequest = null;
  request?.resolve(confirmed);
  // Cancellation can return to the original trigger immediately. A
  // destructive confirmation must keep the focus record until its async
  // mutation removes the trigger, so the caller can resolve the fallback.
  if (!confirmed) restoreDialogFocus();
}
