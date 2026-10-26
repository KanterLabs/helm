// Platform detection and light/dark theme application.
import { helmStorageKeys } from '../lib/state';
import { themeFromMediaPreference } from '../lib/ui';
import { workspace } from './state/workspace.svelte';

export function browserPlatform(): string {
  if (typeof navigator === 'undefined') return '';
  const navigatorWithUserAgentData = navigator as Navigator & { userAgentData?: { platform?: string } };
  return navigatorWithUserAgentData.userAgentData?.platform || navigator.platform || navigator.userAgent || '';
}

export function systemTheme(): 'light' | 'dark' {
  const prefersDark = typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-color-scheme: dark)').matches;
  return themeFromMediaPreference(prefersDark);
}

export function applyTheme() {
  if (typeof document !== 'undefined') {
    document.documentElement.dataset.theme = workspace.theme;
    const meta = document.querySelector('meta[name="theme-color"]');
    meta?.setAttribute('content', workspace.theme === 'dark' ? '#12131a' : '#f8fafc');
  }
}

export function toggleTheme() {
  setTheme(workspace.theme === 'dark' ? 'light' : 'dark');
}

export function setTheme(next: 'light' | 'dark') {
  workspace.theme = next;
  localStorage.setItem(helmStorageKeys.theme, workspace.theme);
  applyTheme();
}
