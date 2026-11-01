// Pure URL/location readers shared by routing and other workspace features.
import { parseTaskRoute } from '../lib/state';

export function getProjectSlugFromLocation(): string {
  const match = window.location.pathname.match(/^\/p\/([^/]+)/);
  return match ? decodeURIComponent(match[1]) : '';
}

export function getTaskRouteFromLocation() {
  return parseTaskRoute(window.location.pathname, window.location.search, window.location.hash);
}

export function isTaskLocation(): boolean {
  return Boolean(getTaskRouteFromLocation());
}

export function isProjectRoadmapLocation(): boolean {
  return /^\/p\/[^/]+\/roadmap\/?$/.test(window.location.pathname);
}

export function isProjectFocusLocation(): boolean {
  return /^\/p\/[^/]+\/focus\/?$/.test(window.location.pathname);
}

export function isProjectReleasesLocation(): boolean {
  return isProjectFocusLocation() || /^\/p\/[^/]+\/releases\/?$/.test(window.location.pathname);
}

export function canonicalizeProjectFocusLocation(): void {
  if (typeof window === 'undefined' || !/^\/p\/[^/]+\/releases\/?$/.test(window.location.pathname)) return;
  const pathname = window.location.pathname.replace(/\/releases\/?$/, '/focus');
  const target = `${pathname}${window.location.search}${window.location.hash}`;
  const current = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  if (current !== target) window.history.replaceState({}, '', target);
}

export function isProjectTimelineLocation(): boolean {
  return /^\/p\/[^/]+\/timeline\/?$/.test(window.location.pathname);
}

export function isProjectAuditLocation(): boolean {
  return /^\/p\/[^/]+\/audits(?:\/[^/]+)?\/?$/.test(window.location.pathname);
}

export function getAuditIdFromLocation(): string {
  const match = window.location.pathname.match(/^\/p\/[^/]+\/audits\/([^/]+)\/?$/);
  return match ? decodeURIComponent(match[1]) : '';
}
