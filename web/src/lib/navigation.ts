import type { NavIconName } from './components/NavIcon.svelte';
import type { Project } from './types';

/** Project-scoped destinations nested under the active project in the sidebar. */
export type ProjectNavView = 'board' | 'timeline' | 'releases' | 'roadmap' | 'audits';

export const projectNavItems: ReadonlyArray<{ view: ProjectNavView; label: string; icon: NavIconName }> = [
  { view: 'board', label: 'Board', icon: 'board' },
  { view: 'timeline', label: 'Timeline', icon: 'timeline' },
  { view: 'releases', label: 'Focus', icon: 'focus' },
  { view: 'roadmap', label: 'Progress', icon: 'progress' },
  { view: 'audits', label: 'Audits', icon: 'audits' }
];

/** Progress is the project-filtered roadmap, so it is active only with a project filter. */
export function isProjectNavItemActive(item: ProjectNavView, view: string, roadmapProjectScoped: boolean): boolean {
  if (item === 'roadmap') return view === 'roadmap' && roadmapProjectScoped;
  return item === view;
}

const viewLabels: Record<string, string> = {
  board: 'Board',
  timeline: 'Timeline',
  releases: 'Focus',
  audits: 'Audits',
  issues: 'Issues',
  tickets: 'Tickets',
  'my-work': 'My work',
  search: 'Search',
  settings: 'Settings',
  admin: 'Metrics'
};

/** The breadcrumb label for the current view. */
export function navViewLabel(view: string, roadmapProjectScoped: boolean): string {
  if (view === 'roadmap') return roadmapProjectScoped ? 'Progress' : 'Roadmap';
  return viewLabels[view] || 'Workspace';
}

export const sidebarProjectLimit = 8;

/**
 * Bounds the non-pinned project list. The active project is always kept
 * visible so the sidebar can show its nested views.
 */
export function sidebarProjectWindow(
  projects: readonly Project[],
  activeSlug: string,
  showAll: boolean,
  limit = sidebarProjectLimit
): { visible: Project[]; hiddenCount: number } {
  if (showAll || projects.length <= limit) return { visible: [...projects], hiddenCount: 0 };
  const visible = projects.slice(0, limit);
  const active = projects.find((project) => project.slug === activeSlug);
  if (active && !visible.includes(active)) visible.push(active);
  return { visible, hiddenCount: projects.length - visible.length };
}

export const sidebarCollapsedStorageKey = 'helm.sidebar-collapsed';

export function readSidebarCollapsed(storage: Pick<Storage, 'getItem'>): boolean {
  try {
    return storage.getItem(sidebarCollapsedStorageKey) === 'true';
  } catch {
    return false;
  }
}

export function writeSidebarCollapsed(storage: Pick<Storage, 'setItem'>, collapsed: boolean): void {
  try {
    storage.setItem(sidebarCollapsedStorageKey, collapsed ? 'true' : 'false');
  } catch {
    // Storage can be unavailable in private modes; the toggle still applies for this session.
  }
}
