// Issue list loading, metrics and issue filter URL state.
import { api, listAllIssues } from '../lib/api';
import { mapWithConcurrency, projectFanOut } from '../lib/concurrency';
import { mergeAuthoritativeTaskList } from '../lib/liveness';
import type { BoardFilters } from '../lib/state';
import { clearedIssueFilterState } from './appHelpers';
import { friendlyError } from './feedback';
import { loadAccessibleReleases } from './releases/load';
import { board } from './state/board.svelte';
import { issues } from './state/issues.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';
import { releaseQueryValue } from './taskQueries';
import { mutationsForRequest } from './taskSync';

/** Load bug-capable tasks from every accessible project for the Issues view. */
export async function loadIssues(): Promise<boolean> {
  const requestId = ++issues.issueRequest;
  const requestedSession = session.sessionGeneration;
  const mutationSnapshot = board.taskMutationRevision;
  issues.issuesLoading = true;
  issues.issuesError = '';
  try {
    const [taskResult, columnResults] = await Promise.all([
      listAllIssues({ limit: 200, release_id: releaseQueryValue(issues.issueReleaseFilter) || (issues.issueReleaseFilter === 'unassigned' ? issues.issueReleaseFilter : undefined) }),
      mapWithConcurrency(workspace.projects, projectFanOut, (project) => api.listAllColumns(project.id))
    ]);
    if (
      requestId !== issues.issueRequest
      || workspace.view !== 'issues'
      || session.sessionGeneration !== requestedSession
      || !session.user
    ) return false;
    issues.issueTasks = mergeAuthoritativeTaskList(
      issues.issueTasks,
      taskResult.data,
      mutationsForRequest('issues', mutationSnapshot)
    );
    issues.issueColumns = columnResults.flatMap((result) => result.data);
    void loadAccessibleReleases();
    return true;
  } catch (error) {
    if (requestId === issues.issueRequest && workspace.view === 'issues' && session.sessionGeneration === requestedSession && session.user) {
      issues.issuesError = friendlyError(error, 'Issues could not be loaded.');
    }
    return false;
  } finally {
    if (requestId === issues.issueRequest && session.sessionGeneration === requestedSession) issues.issuesLoading = false;
  }
}

export async function loadIssueMetrics(): Promise<boolean> {
  const requestId = ++issues.issueMetricsRequest;
  const requestedSession = session.sessionGeneration;
  issues.issueMetricsStatus = 'loading';
  try {
    const result = await api.issueMetrics();
    if (requestId !== issues.issueMetricsRequest || session.sessionGeneration !== requestedSession || !session.user) return false;
    issues.issueMetricsData = result;
    issues.issueMetricsStatus = 'known';
    return true;
  } catch {
    if (requestId === issues.issueMetricsRequest && session.sessionGeneration === requestedSession && session.user) {
      issues.issueMetricsStatus = 'error';
    }
    return false;
  }
}

export function clearIssueFilters() {
  const cleared = clearedIssueFilterState();
  issues.issueFilters = cleared.filters;
  issues.issueProjectFilter = cleared.project;
  issues.issueReleaseFilter = cleared.release;
  syncIssueViewURL(issues.issueFilters, issues.issueProjectFilter, issues.issueReleaseFilter);
  void loadIssues();
}

export function applyIssueRouteFilters(params: URLSearchParams) {
  const value = (name: string) => params.get(name)?.trim() || '';
  const priority = value('priority');
  const state = value('state');
  const severity = value('severity');
  const resolution = value('resolution');
  issues.issueFilters = {
    ...issues.issueFilters,
    query: value('q'),
    priority: ['low', 'normal', 'high', 'urgent'].includes(priority) ? priority : 'all',
    state: ['backlog', 'ready', 'active', 'blocked', 'completed'].includes(state) ? state : 'all',
    assignee: value('assignee') || 'all',
    severity: ['s1', 's2', 's3', 's4', 'untriaged'].includes(severity) ? severity : 'all',
    reporter: value('reporter') || 'all',
    resolution: ['fixed', 'duplicate', 'not_planned', 'cannot_reproduce', 'works_as_designed', 'open'].includes(resolution) ? resolution : 'all'
  };
  issues.issueProjectFilter = value('project') || 'all';
  issues.issueReleaseFilter = value('release_id') || 'all';
}

export function syncIssueViewURL(current: BoardFilters, project: string, release = issues.issueReleaseFilter) {
  if (typeof window === 'undefined' || !/^\/issues\/?$/.test(window.location.pathname)) return;
  const params = new URLSearchParams();
  if (current.query) params.set('q', current.query);
  if (current.priority !== 'all') params.set('priority', current.priority);
  if (current.state !== 'all') params.set('state', current.state);
  if (current.assignee !== 'all') params.set('assignee', current.assignee);
  if (current.severity && current.severity !== 'all') params.set('severity', current.severity);
  if (current.reporter && current.reporter !== 'all') params.set('reporter', current.reporter);
  if (current.resolution && current.resolution !== 'all') params.set('resolution', current.resolution);
  if (project !== 'all') params.set('project', project);
  if (release !== 'all') params.set('release_id', release);
  const query = params.toString();
  const target = `/issues${query ? `?${query}` : ''}`;
  if (`${window.location.pathname}${window.location.search}` !== target) window.history.replaceState({}, '', target);
}
