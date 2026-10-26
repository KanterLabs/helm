<svelte:options runes={true} />

<script lang="ts">
  // Global task search and saved views.
  import { priorityLabels, stateLabels } from '../../constants';
  import { openWorkTask } from '../../drawer/open';
  import { loadSearch, newSearch, openSavedView, removeCurrentSearchView, renameCurrentSearchView, runSearch, saveCurrentSearchView, updateCurrentSearchView } from '../../search';
  import { releasePlan } from '../../state/releasePlan.svelte';
  import { search } from '../../state/search.svelte';
  import { session } from '../../state/session.svelte';
  import { projectForTask, releaseOptionLabel, taskReleaseName } from '../../taskQueries';
</script>

<section class="page-heading search-heading">
  <div><div class="breadcrumbs"><span>Workspace</span><span>/</span><span>Search</span>{#if search.searchView}<span>/</span><span>{search.searchView.name}</span>{/if}</div><h1>{search.searchView ? search.searchView.name : 'Search everything'}</h1><p>Find tasks across every project you can access, then open the board context in one click.</p></div>
  <div class="heading-actions"><button class="button quiet-button" type="button" on:click={newSearch}>New search</button><button class="button quiet-button" type="button" on:click={() => loadSearch()}>↻ Refresh</button></div>
</section>
<section class="search-workspace" aria-label="Global task search">
    <form class="search-toolbar" on:submit|preventDefault={runSearch}>
      <div class="filter-search"><span aria-hidden="true">⌕</span><input aria-label="Search all tasks" bind:value={search.searchQuery} placeholder="Search tasks, keys, labels, projects…" /></div>
    <select aria-label="Filter search by state" bind:value={search.searchState} on:change={runSearch}><option value="all">All states</option>{#each Object.entries(stateLabels) as pair}<option value={pair[0]}>{pair[1]}</option>{/each}</select>
    <select aria-label="Filter search by priority" bind:value={search.searchPriority} on:change={runSearch}><option value="all">All priorities</option>{#each Object.entries(priorityLabels) as pair}<option value={pair[0]}>{pair[1]}</option>{/each}</select>
    <select aria-label="Filter search by focus" bind:value={search.searchReleaseFilter} on:change={runSearch}><option value="all">All focus areas</option>{#each releasePlan.allReleaseOptions as release}<option value={release.id}>{releaseOptionLabel(release)}</option>{/each}<option value="unassigned">No focus</option></select><select aria-label="Sort search results" bind:value={search.searchSortField} on:change={runSearch}><option value="updated_at">Recently updated</option><option value="due_at">Due date</option><option value="priority">Priority</option><option value="title">Title</option><option value="key">Task key</option></select>
    <select aria-label="Sort search direction" bind:value={search.searchSortDirection} on:change={runSearch}><option value="desc">Descending</option><option value="asc">Ascending</option></select>
    <button class="button primary" type="submit">Search</button>
  </form>
  <section class="saved-view-panel" aria-label="Saved views">
    <div class="saved-view-heading"><div><span class="eyebrow">Reusable filters</span><h2>Saved views</h2><p>Save this search and share it with your workspace.</p></div><div class="saved-view-save"><input aria-label="Saved view name" bind:value={search.savedViewName} placeholder="Name this view" /><label class="check-label"><input type="checkbox" bind:checked={search.savedViewShared} /><span>Share</span></label>{#if search.searchView}<span role="status">{search.searchViewDirty ? 'Unsaved changes' : 'Saved'}</span>{#if search.searchView.owner_id === session.user?.id}<button class="button quiet-button" type="button" disabled={search.savedViewSaving || !search.searchViewDirty} on:click={updateCurrentSearchView}>Update view</button><button class="button quiet-button" type="button" disabled={search.savedViewSaving || !search.savedViewName.trim() || search.savedViewName.trim() === search.searchView.name} on:click={renameCurrentSearchView}>Rename</button>{/if}{/if}<button class="button quiet-button" type="button" disabled={search.savedViewSaving} on:click={saveCurrentSearchView}>{search.searchView ? 'Save as' : 'Save view'}</button></div></div>
    {#if search.searchSavedViews.length}<div class="saved-view-list">{#each search.searchSavedViews as saved (saved.id)}<div class="saved-view-item"><button class:active={search.searchViewId === saved.id} class="saved-view-chip" type="button" on:click={() => openSavedView(saved)}><span>☆</span><span><strong>{saved.name}</strong><small>{saved.shared ? 'Shared' : 'Private'}</small></span></button>{#if saved.owner_id === session.user?.id}<button class="saved-view-delete" type="button" aria-label={`Delete saved view ${saved.name}`} on:click={() => removeCurrentSearchView(saved)}>×</button>{/if}</div>{/each}</div>{/if}
  </section>
  {#if search.searchError}<div class="inline-alert error content-alert" role="alert"><span>!</span><span>{search.searchError}</span><button class="text-button" type="button" on:click={() => loadSearch()}>Retry</button></div>{/if}
  {#if search.searchLoading}<div class="list-skeleton" aria-label="Loading search results"><div></div><div></div><div></div></div>{:else if !search.visibleSearchTasks.length}<div class="empty-state search-empty"><div class="empty-icon">⌕</div><h2>No matching tasks</h2><p>Try a task key, project name, label, or a shorter phrase.</p></div>{:else}<section class="search-results" aria-label="Search results">{#each search.visibleSearchTasks as task (task.id)}<button class="search-result-row" type="button" on:click={() => openWorkTask(task)}><span class="work-project-dot" style={`--project-color: ${projectForTask(task)?.color || '#6d5efc'}`}></span><span class="search-result-main"><span class="work-row-top"><span class="task-key">{task.key}</span><span class={`priority-pill priority-${task.priority}`}>{priorityLabels[task.priority]}</span>{#if taskReleaseName(task)}<span class="release-chip">{taskReleaseName(task)}</span>{/if}</span><strong>{task.title}</strong><span class="work-project-name">{projectForTask(task)?.name || 'Project'}</span></span><span class="search-result-column">{search.searchColumnsByProject[task.project_id]?.find((column) => column.id === task.column_id)?.name || '—'}</span><span class="row-arrow">→</span></button>{/each}</section>{#if search.searchNextCursor}<div class="search-pagination"><button class="button quiet-button" type="button" on:click={() => loadSearch(true)}>Load more results</button></div>{/if}{/if}
</section>
