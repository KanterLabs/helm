// Sidebar, rail, mobile drawer and navigation menus.
import { tick } from 'svelte';
import { writeSidebarCollapsed } from '../../lib/navigation';
import type { ProjectNavView } from '../../lib/navigation';
import type { ProjectOrderMode } from '../../lib/state';
import type { Project } from '../../lib/types';
import { closeBetaSwitcher } from '../beta';
import { openBugModal } from '../bugModal';
import { openProjectModal } from '../creation';
import { openProjectReleases } from '../releases/load';
import { openProjectRoadmap } from '../roadmap';
import { openProjectAudits, openProjectTimeline, selectProject, setView } from '../routing';
import { beta } from '../state/beta.svelte';
import { nav } from '../state/nav.svelte';
import { workspace } from '../state/workspace.svelte';
import { openTaskModal } from '../taskModal';

export const projectOrderOptions: ReadonlyArray<{ mode: ProjectOrderMode; label: string }> = [
  { mode: 'smart', label: 'Smart' },
  { mode: 'recent', label: 'Recent' },
  { mode: 'alphabetical', label: 'A–Z' }
];

export function toggleSidebarCollapsed() {
  nav.sidebarCollapsed = !nav.sidebarCollapsed;
  writeSidebarCollapsed(localStorage, nav.sidebarCollapsed);
  closeNavMenus(false);
}

/** Closes the sidebar, account, create, and order menus. */
export function closeNavMenus(restoreFocus = true) {
  const trigger = nav.accountMenuOpen ? nav.accountMenuTrigger : nav.createMenuOpen ? nav.createMenuTrigger : nav.projectOrderMenuOpen ? nav.projectOrderTrigger : null;
  nav.accountMenuOpen = false;
  nav.createMenuOpen = false;
  nav.projectOrderMenuOpen = false;
  if (restoreFocus) trigger?.focus();
}

export function closeMobileNav(restoreFocus = true) {
  if (!nav.mobileNavOpen) return;
  nav.mobileNavOpen = false;
  if (restoreFocus) void tick().then(() => nav.mobileMoreTrigger?.focus());
}

export function toggleMobileNav() {
  if (nav.mobileNavOpen) {
    closeMobileNav(false);
    return;
  }
  closeNavMenus(false);
  nav.projectSwitcherOpen = false;
  closeBetaSwitcher(false);
  nav.mobileNavOpen = true;
  void tick().then(() => nav.sidebarElement?.querySelector<HTMLElement>('.nav-links button')?.focus());
}

export function toggleNavMenu(menu: 'account' | 'create' | 'order') {
  const wasOpen = menu === 'account' ? nav.accountMenuOpen : menu === 'create' ? nav.createMenuOpen : nav.projectOrderMenuOpen;
  closeNavMenus(false);
  nav.projectSwitcherOpen = false;
  closeBetaSwitcher(false);
  if (wasOpen) return;
  if (menu === 'account') nav.accountMenuOpen = true;
  else if (menu === 'create') nav.createMenuOpen = true;
  else nav.projectOrderMenuOpen = true;
  void tick().then(() => {
    const element = menu === 'account' ? nav.accountMenuElement : menu === 'create' ? nav.createMenuElement : nav.projectOrderMenuElement;
    element?.querySelector<HTMLElement>('[role^="menuitem"]:not(:disabled)')?.focus();
  });
}

export function navMenuKeydown(event: KeyboardEvent) {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  const menu = event.currentTarget instanceof HTMLElement ? event.currentTarget : null;
  const items = menu ? Array.from(menu.querySelectorAll<HTMLElement>('[role^="menuitem"]:not(:disabled)')) : [];
  if (!items.length) return;
  event.preventDefault();
  const index = items.indexOf(document.activeElement as HTMLElement);
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length;
  items[next].focus();
}

/** Runs a navigation action from the sidebar, closing overlays first. */
export function navTo(action: () => unknown) {
  closeNavMenus(false);
  closeMobileNav(false);
  nav.railTip = null;
  void action();
}

export function sidebarProjectClick(project: Project) {
  if (workspace.activeProjectSlug === project.slug) {
    if (nav.projectScopedView && !nav.sidebarRail) {
      nav.projectTreeCollapsed = !nav.projectTreeCollapsed;
      return;
    }
    nav.projectTreeCollapsed = false;
    navTo(() => setView('board'));
    return;
  }
  nav.projectTreeCollapsed = false;
  navTo(() => selectProject(project));
}

export function openProjectNavItem(item: ProjectNavView) {
  navTo(() => item === 'board' ? setView('board')
    : item === 'timeline' ? openProjectTimeline()
      : item === 'releases' ? openProjectReleases()
        : item === 'roadmap' ? openProjectRoadmap()
          : openProjectAudits());
}

export function createFromMenu(kind: 'task' | 'bug' | 'project') {
  closeNavMenus(false);
  if (kind === 'task') void openTaskModal();
  else if (kind === 'bug') void openBugModal();
  else openProjectModal();
}

export function showRailTip(event: Event) {
  if (!nav.sidebarRail) return;
  const target = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-nav-tip]') : null;
  if (!target || !nav.sidebarElement?.contains(target)) {
    nav.railTip = null;
    return;
  }
  const rect = target.getBoundingClientRect();
  nav.railTip = { text: target.dataset.navTip || '', kbd: target.dataset.navKbd || '', top: rect.top + rect.height / 2, left: rect.right + 10 };
}

export function handleProjectSwitcherPointerDown(event: PointerEvent) {
  const target = event.target;
  if (!(target instanceof Node)) return;
  if (nav.projectSwitcherOpen && !nav.projectSwitcherPopover?.contains(target) && !nav.projectPickerTrigger?.contains(target)) {
    nav.projectSwitcherOpen = false;
  }
  if (beta.betaSwitcherOpen && !beta.betaSwitcherMenu?.contains(target) && !beta.betaSwitcherTrigger?.contains(target)) {
    closeBetaSwitcher();
  }
  if (nav.accountMenuOpen && !nav.accountMenuElement?.contains(target) && !nav.accountMenuTrigger?.contains(target)) nav.accountMenuOpen = false;
  if (nav.createMenuOpen && !nav.createMenuElement?.contains(target) && !nav.createMenuTrigger?.contains(target)) nav.createMenuOpen = false;
  if (nav.projectOrderMenuOpen && !nav.projectOrderMenuElement?.contains(target) && !nav.projectOrderTrigger?.contains(target)) nav.projectOrderMenuOpen = false;
}
