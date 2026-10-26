import { tick } from 'svelte';

const focusableSelector = [
  'a[href]',
  'area[href]',
  'button:not(:disabled)',
  'input:not(:disabled):not([type="hidden"])',
  'select:not(:disabled)',
  'textarea:not(:disabled)',
  '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])'
].join(',');

function isVisible(element: HTMLElement): boolean {
  if (!element.isConnected || element.hasAttribute('hidden') || element.getAttribute('aria-hidden') === 'true') return false;
  const style = window.getComputedStyle(element);
  return style.display !== 'none' && style.visibility !== 'hidden';
}

export function focusTrap(node: HTMLElement) {
  const handleKeydown = (event: KeyboardEvent) => {
    if (event.key !== 'Tab') return;
    const focusable = Array.from(node.querySelectorAll<HTMLElement>(focusableSelector)).filter(isVisible);
    if (!focusable.length) {
      event.preventDefault();
      node.focus();
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };
  node.addEventListener('keydown', handleKeydown);
  return { destroy: () => node.removeEventListener('keydown', handleKeydown) };
}

export function createAuditFocus() {
  let modalReturnFocus: HTMLElement | null = null;
  let destroyTimer: number | undefined;

  function rememberModalFocus(): void {
    if (typeof document === 'undefined') return;
    const active = document.activeElement;
    modalReturnFocus = active instanceof HTMLElement ? active : null;
  }

  function restoreModalFocus(): void {
    const target = modalReturnFocus;
    modalReturnFocus = null;
    if (!target) return;
    void tick().then(() => {
      if (target.isConnected && !target.hasAttribute('disabled')) target.focus();
    });
  }

  function announce(message: string): void {
    const node = document.querySelector<HTMLElement>('[data-audit-live-region]');
    if (!node) return;
    node.textContent = '';
    window.clearTimeout(destroyTimer);
    destroyTimer = window.setTimeout(() => { node.textContent = message; }, 20);
  }

  return { rememberModalFocus, restoreModalFocus, announce, destroy: () => window.clearTimeout(destroyTimer) };
}
