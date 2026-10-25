// Reactive state: Issue list, metrics and filters.
import { bugReporterId, filterTasks } from '../../lib/state';
import type { BoardFilters } from '../../lib/state';
import type { Column, IssueMetrics, Task } from '../../lib/types';
import { taskMatchesReleaseFilter } from '../taskQueries';
import type { CountStatus } from '../types';

class IssuesState {
  issueTasks = $state.raw<Task[]>([]);
  issueColumns = $state.raw<Column[]>([]);
  issuesLoading = $state.raw(false);
  issuesError = $state.raw('');
  issueRequest = $state.raw(0);
  issueMetricsData = $state.raw<IssueMetrics | null>(null);
  issueMetricsStatus = $state.raw<CountStatus>('unknown');
  issueMetricsRequest = $state.raw(0);
  issueFilters = $state.raw<BoardFilters>({
    query: '',
    priority: 'all',
    label: 'all',
    assignee: 'all',
    state: 'all',
    kind: 'bug',
    severity: 'all',
    reporter: 'all',
    resolution: 'all'
  });
  issueProjectFilter = $state.raw('all');
  issueReleaseFilter = $state.raw('all');
  issueSearchInput = $state.raw<HTMLInputElement | null>(null);

  // Derived values
  visibleIssues = $derived(filterTasks(
    issues.issueTasks.filter((task) => issues.issueProjectFilter === 'all' || task.project_id === issues.issueProjectFilter),
    issues.issueColumns,
    issues.issueFilters
  ).filter((task) => taskMatchesReleaseFilter(task, issues.issueReleaseFilter)));
  issueReporterOptions = $derived(Array.from(new Set(issues.issueTasks.map((task) => bugReporterId(task)).filter(Boolean))).sort());
  issueHealthMetrics = $derived({
    open: issues.issueTasks.filter((task) => !task.bug?.resolution).length,
    untriaged: issues.issueTasks.filter((task) => !task.bug?.severity && !task.bug?.resolution).length,
    severe: issues.issueTasks.filter((task) => !task.bug?.resolution && (task.bug?.severity === 's1' || task.bug?.severity === 's2')).length,
    recentlyResolved: issues.issueTasks.filter((task) => task.bug?.resolved_at && Date.now() - Date.parse(task.bug.resolved_at) <= 7 * 24 * 60 * 60 * 1000).length,
    reopened: issues.issueMetricsStatus === 'known' ? issues.issueMetricsData?.reopened ?? null : null
  });
}

export const issues = new IssuesState();
