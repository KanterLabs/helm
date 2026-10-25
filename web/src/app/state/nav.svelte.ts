// Reactive state: Sidebar, rail, mobile drawer, menus and sidebar counts.
import { navViewLabel, sidebarProjectWindow } from '../../lib/navigation';
import type { SidebarCounts } from '../../lib/types';
import type { CountStatus } from '../types';
import { roadmapData } from './roadmapData.svelte';
import { workspace } from './workspace.svelte';

class NavState {
  projectSwitcherOpen = $state.raw(false);
  projectSwitcherQuery = $state.raw('');
  projectPickerTrigger = $state.raw<HTMLButtonElement | null>(null);
  projectSwitcherPopover = $state.raw<HTMLDivElement | null>(null);
  // Sidebar and site navigation chrome (docs: TC-277). Only one nav overlay
  // is open at a time; each records its trigger so Escape can restore focus.
  sidebarCollapsed = $state.raw(false);
  narrowSidebar = $state.raw(false);
  mobileNavOpen = $state.raw(false);
  accountMenuOpen = $state.raw(false);
  createMenuOpen = $state.raw(false);
  projectOrderMenuOpen = $state.raw(false);
  projectTreeCollapsed = $state.raw(false);
  showAllProjects = $state.raw(false);
  railTip = $state.raw<{ text: string; kbd: string; top: number; left: number } | null>(null);
  accountMenuTrigger = $state.raw<HTMLButtonElement | null>(null);
  accountMenuElement = $state.raw<HTMLDivElement | null>(null);
  createMenuTrigger = $state.raw<HTMLButtonElement | null>(null);
  createMenuElement = $state.raw<HTMLDivElement | null>(null);
  projectOrderTrigger = $state.raw<HTMLButtonElement | null>(null);
  projectOrderMenuElement = $state.raw<HTMLDivElement | null>(null);
  mobileMoreTrigger = $state.raw<HTMLButtonElement | null>(null);
  sidebarElement = $state.raw<HTMLElement | null>(null);
  sidebarCounts = $state.raw<SidebarCounts | null>(null);
  sidebarCountsStatus = $state.raw<CountStatus>('unknown');
  sidebarCountsRequest = $state.raw(0);

  // Derived values
  sidebarRail = $derived(!nav.mobileNavOpen && (nav.sidebarCollapsed || nav.narrowSidebar));
  projectScopedView = $derived(workspace.view === 'board' || workspace.view === 'timeline' || workspace.view === 'releases' || workspace.view === 'audits' || (workspace.view === 'roadmap' && Boolean(roadmapData.roadmapProjectId)));
  projectWindow = $derived(sidebarProjectWindow(workspace.orderedProjects.filter((project) => !project.favorite), workspace.activeProjectSlug, nav.showAllProjects));
  currentViewLabel = $derived(navViewLabel(workspace.view, Boolean(roadmapData.roadmapProjectId)));
  filteredSwitcherProjects = $derived(workspace.orderedProjects.filter((project) =>
    `${project.name} ${project.key}`.toLowerCase().includes(nav.projectSwitcherQuery.trim().toLowerCase())
  ));
}

export const nav = new NavState();
