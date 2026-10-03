import { writable } from 'svelte/store';

/** The help page open in the in-app drawer, or null. */
export const helpRequest = writable<{ page: string; anchor?: string } | null>(null);

/** Opens an embedded user guide (docs/*.md) at an optional heading id. */
export function openHelp(page: string, anchor = ''): void {
  helpRequest.set({ page, anchor });
}
