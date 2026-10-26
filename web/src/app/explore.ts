// Roadmap explore mode: hide the app chrome and enlarge the roadmap. The mode
// lives in the URL so a reload, or returning from a task, keeps it.
import { tick } from 'svelte';
import { explore } from './state/explore.svelte';

export const exploreZoomLevels = [1, 1.15, 1.3, 1.5, 1.75] as const;
export const exploreZoomStorageKey = 'helm.roadmap-explore-zoom';

export function exploreLocation(): boolean {
  return /^\/(?:p\/[^/]+\/)?roadmap\/?$/.test(window.location.pathname)
    && new URLSearchParams(window.location.search).get('explore') === '1';
}

/** Re-read the mode after any view or history change. */
export function syncExploreFromLocation() {
  explore.active = exploreLocation();
}

export function readExploreZoom(storage: Storage) {
  const stored = Number(storage.getItem(exploreZoomStorageKey));
  explore.zoom = exploreZoomLevels.find((level) => level === stored) ?? 1.15;
}

function replaceExploreParam(on: boolean) {
  const url = new URL(window.location.href);
  if (on) url.searchParams.set('explore', '1');
  else url.searchParams.delete('explore');
  window.history.replaceState(window.history.state, '', `${url.pathname}${url.search}${url.hash}`);
}

export async function enterExplore() {
  replaceExploreParam(true);
  explore.active = true;
  await tick();
  explore.exitButton?.focus();
}

export async function exitExplore() {
  replaceExploreParam(false);
  explore.active = false;
  if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
  await tick();
  explore.trigger?.focus();
}

export function stepExploreZoom(direction: -1 | 1) {
  const index = exploreZoomLevels.indexOf(explore.zoom as typeof exploreZoomLevels[number]);
  const next = exploreZoomLevels[Math.min(exploreZoomLevels.length - 1, Math.max(0, index + direction))];
  if (next === explore.zoom) return;
  explore.zoom = next;
  localStorage.setItem(exploreZoomStorageKey, String(next));
}

export function toggleExploreFullscreen() {
  if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
  else void document.documentElement.requestFullscreen?.().catch(() => undefined);
}
