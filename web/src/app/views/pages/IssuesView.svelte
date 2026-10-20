<svelte:options runes={true} />

<script lang="ts">
  // Cross-project issue list and filters.
  import { openBugModal } from '../../bugModal';
  import { priorityLabels, resolutionLabels, resolutionOptions, severityLabels } from '../../constants';
  import { openWorkTask } from '../../drawer/open';
  import { clearIssueFilters, loadIssues, syncIssueViewURL } from '../../issues';
  import { issues } from '../../state/issues.svelte';
  import { releasePlan } from '../../state/releasePlan.svelte';
  import { workspace } from '../../state/workspace.svelte';
  import { projectForTask, releaseOptionLabel, taskReleaseName } from '../../taskQueries';
</script>

<section class="page-heading issues-heading">
  <div>
    <div class="breadcrumbs"><span>Workspace</span><span>/</span><span>Issues</span></div>
    <h1>Issues</h1>
    <p>Report, triage, and resolve bugs across the projects you can access.</p>
  </div>
  <div class="heading-actions">
    <button class="button quiet-button" type="button" on:click={loadIssues}>↻ Refresh</button>
    <button class="button primary" type="button" data-report-bug-trigger on:click={openBugModal}><span aria-hidden="true">＋</span> Report bug</button>
  </div>
</section>
<section class="issues-toolbar" aria-label="Issue filters">
  <div class="filter-search"><span aria-hidden="true">⌕</span><input bind:this={issues.issueSearchInput} aria-label="Search issues" bind:value={() => issues.issueFilters.query, (value) => issues.issueFilters = { ...issues.issueFilters, query: value }} placeholder="Search issues…" /><kbd>/</kbd></div>
  <div class="filter-group">
    <select aria-label="Filter by issue type" bind:value={() => issues.issueFilters.kind, (value) => issues.issueFilters = { ...issues.issueFilters, kind: value }}><option value="bug">Bugs</option><option value="task">Tasks</option><option value="all">All kinds</option></select>
    <select aria-label="Filter by severity" bind:value={() => issues.issueFilters.severity, (value) => issues.issueFilters = { ...issues.issueFilters, severity: value }}><option value="all">All severities</option><option value="untriaged">Untriaged</option>{#each Object.entries(severityLabels) as pair}<option value={pair[0]}>{pair[1]}</option>{/each}</select>
    <select aria-label="Filter by resolution" bind:value={() => issues.issueFilters.resolution, (value) => issues.issueFilters = { ...issues.issueFilters, resolution: value }}><option value="all">All resolutions</option><option value="open">Open</option>{#each resolutionOptions as resolution}<option value={resolution}>{resolutionLabels[resolution]}</option>{/each}</select>
    <select aria-label="Filter issues by project" bind:value={issues.issueProjectFilter}><option value="all">All projects</option>{#each workspace.navProjects as project}<option value={project.id}>{project.key} · {project.name}</option>{/each}</select>
    <select aria-label="Filter issues by focus" bind:value={issues.issueReleaseFilter} on:change={() => { syncIssueViewURL(issues.issueFilters, issues.issueProjectFilter); void loadIssues(); }}><option value="all">All focus areas</option>{#each releasePlan.allReleaseOptions as release}<option value={release.id}>{releaseOptionLabel(release)}</option>{/each}<option value="unassigned">No focus</option></select><select aria-label="Filter by reporter" bind:value={() => issues.issueFilters.reporter, (value) => issues.issueFilters = { ...issues.issueFilters, reporter: value }}><option value="all">All reporters</option>{#each issues.issueReporterOptions as reporter}<option value={reporter}>{reporter}</option>{/each}</select>
  </div>
  {#if issues.issueFilters.query || issues.issueFilters.kind !== 'bug' || issues.issueFilters.severity !== 'all' || issues.issueFilters.resolution !== 'all' || issues.issueFilters.reporter !== 'all' || issues.issueProjectFilter !== 'all' || issues.issueReleaseFilter !== 'all'}<button class="clear-filters" type="button" on:click={clearIssueFilters}>Clear filters</button>{/if}
  <span class="toolbar-spacer"></span><span class="task-total">{issues.visibleIssues.length} {issues.visibleIssues.length === 1 ? 'issue' : 'issues'}</span>
</section>
<section class="issue-metrics" aria-label="Issue health">
  <article><span>Open</span><strong>{issues.issueHealthMetrics.open}</strong></article>
  <article><span>Untriaged</span><strong>{issues.issueHealthMetrics.untriaged}</strong></article>
  <article><span>S1 / S2 open</span><strong>{issues.issueHealthMetrics.severe}</strong></article>
  <article><span>Resolved · 7d</span><strong>{issues.issueHealthMetrics.recentlyResolved}</strong></article>
  <article><span>Reopened · 7d</span><strong>{issues.issueHealthMetrics.reopened === null ? '—' : issues.issueHealthMetrics.reopened}</strong></article>
</section>
{#if issues.issuesError}<div class="inline-alert error content-alert" role="alert"><span>!</span><span>{issues.issuesError}</span><button class="text-button" type="button" on:click={loadIssues}>Retry</button></div>{/if}
{#if issues.issuesLoading}<div class="list-skeleton" aria-label="Loading issues">{#each [1, 2, 3] as item}<div></div>{/each}</div>
{:else if !issues.visibleIssues.length}
  <div class="empty-state issues-empty"><div class="empty-icon">⚠</div><h2>No issues match these filters</h2><p>Report a bug when something behaves differently than expected, then keep its status visible here.</p><button class="button primary" type="button" data-report-bug-trigger on:click={openBugModal}>＋ Report a bug</button></div>
{:else}
  <section class="issues-list" aria-label="Reported issues">
    {#each issues.visibleIssues as issue (issue.id)}
      <button class="issue-row" type="button" on:click={() => openWorkTask(issue)}>
        <span class="issue-kind-badge" class:task-kind={issue.kind !== 'bug'}>{issue.kind === 'bug' ? 'Bug' : 'Task'}</span>
        <span class="issue-main"><span class="issue-row-top"><span class="task-key">{issue.key}</span><span class={`priority-pill priority-${issue.priority}`}>{priorityLabels[issue.priority]}</span>{#if taskReleaseName(issue)}<span class="release-chip">{taskReleaseName(issue)}</span>{/if}</span><strong>{issue.title}</strong><span class="issue-project-name">{projectForTask(issue)?.name || 'Project'}{#if issue.bug?.reporter_id}<span> · Reported by {issue.bug.reporter_id}</span>{/if}</span></span>
        <span class="issue-status"><span class:untriaged={!issue.bug?.severity} class="severity-badge">{issue.bug?.severity ? severityLabels[issue.bug.severity] : 'Untriaged'}</span>{#if issue.bug?.resolution}<span class="resolution-badge">{resolutionLabels[issue.bug.resolution] || issue.bug.resolution}</span>{:else}<span class="resolution-badge open">Open</span>{/if}</span>
        <span class="issue-column">{issues.issueColumns.find((column) => column.project_id === issue.project_id && column.id === issue.column_id)?.name || '—'}</span><span class="row-arrow">→</span>
      </button>
    {/each}
  </section>
{/if}
