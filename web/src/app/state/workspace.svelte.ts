// Reactive state: Current view, theme, projects and project ordering.
import type TicketsWorkspace from '../../lib/components/TicketsWorkspace.svelte';
import { orderProjects } from '../../lib/state';
import type { ProjectOrderMode } from '../../lib/state';
import type { Project, ProjectIntelligenceResponse } from '../../lib/types';
import type { View } from '../types';

class WorkspaceState {
  ticketsWorkspace = $state.raw<TicketsWorkspace | null>(null);
  theme = $state.raw<'light' | 'dark'>('light');
  view = $state.raw<View>('board');
  projects = $state.raw<Project[]>([]);
  activeProjectSlug = $state.raw('');
  auditIdFromRoute = $state.raw('');
  recentProjectIds = $state.raw<string[]>([]);
  projectsLoading = $state.raw(false);
  projectsError = $state.raw('');
  projectOrderMode = $state.raw<ProjectOrderMode>('smart');
  projectIntelligence = $state.raw<ProjectIntelligenceResponse | null>(null);
  projectIntelligenceLoading = $state.raw(false);
  projectIntelligenceAnalyzing = $state.raw(false);
  projectIntelligenceError = $state.raw('');
  projectIntelligenceController = $state.raw<AbortController | undefined>();
  projectListRequest = $state.raw(0);
  projectSwitchVersion = $state.raw(0);
  ticketsDirty = $state.raw(false);
  auditRefreshToken = $state.raw(0);

  // Derived values
  activeProject: Project | undefined = $derived(workspace.projects.find((project) => project.slug === workspace.activeProjectSlug));
  intelligenceProjectIds = $derived(workspace.projectIntelligence?.projects.map((project) => project.project_id) || []);
  projectIntelligenceById = $derived(new Map((workspace.projectIntelligence?.projects || []).map((project) => [project.project_id, project])));
  // The built-in ticket queue is reached through Tickets, never as a project
  // in navigation or pickers (docs/TICKET_QUEUE_PLAN.md).
  navProjects = $derived(workspace.projects.filter((project) => !project.system_kind));
  orderedProjects = $derived(orderProjects(workspace.navProjects, workspace.projectOrderMode, workspace.intelligenceProjectIds, workspace.recentProjectIds));
  favoriteProjects = $derived(workspace.orderedProjects.filter((project) => project.favorite));
  recentProjects = $derived(workspace.recentProjectIds
    .map((id) => workspace.navProjects.find((project) => project.id === id))
    .filter((project): project is Project => Boolean(project)));
}

export const workspace = new WorkspaceState();
