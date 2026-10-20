<svelte:options runes={true} />

<script lang="ts">
  // Project board and timeline view.
  import BoardTimeline from '../../../lib/components/BoardTimeline.svelte';
  import { loadBoard } from '../../board/load';
  import { loadBoardTimeline, loadOlderBoardTimeline, setBoardTimelineFilter } from '../../boardTimeline';
  import { openBugModal } from '../../bugModal';
  import { openProjectModal } from '../../creation';
  import { openBoardTimelineTask } from '../../drawer/open';
  import { handlePortableFile } from '../../portable';
  import { toggleFavorite } from '../../projects';
  import { board } from '../../state/board.svelte';
  import { portable } from '../../state/portable.svelte';
  import { timeline } from '../../state/timeline.svelte';
  import { workspace } from '../../state/workspace.svelte';
  import { openTaskModal } from '../../taskModal';
  import BoardColumns from './BoardColumns.svelte';
  import BoardToolbar from './BoardToolbar.svelte';
</script>

{#if workspace.projectsLoading && !workspace.projects.length}
  <section class="workspace-loading" role="status" aria-label="Loading workspace" aria-live="polite" aria-busy="true">
    <span class="sr-only">Loading projects and board…</span>
    <div class="workspace-loading-heading" aria-hidden="true">
      <span class="loading-line loading-line-short"></span>
      <span class="loading-line loading-line-title"></span>
      <span class="loading-line loading-line-copy"></span>
    </div>
    <div class="board board-loading" aria-hidden="true">{#each [1, 2, 3, 4] as item}<div class="column-skeleton"><div></div><div></div><div></div></div>{/each}</div>
  </section>
{:else if workspace.activeProject}
  <section class="page-heading board-heading">
    <div><div class="breadcrumbs"><span>Workspace</span><span>/</span><span>{workspace.activeProject.key}</span></div><div class="heading-title-row"><span class="heading-project-dot" style={`--project-color: ${workspace.activeProject.color || '#6d5efc'}`}></span><h1>{workspace.activeProject.name}</h1><button class="icon-button favorite-heading" class:starred={workspace.activeProject.favorite} type="button" aria-label={workspace.activeProject.favorite ? 'Remove from favorites' : 'Add to favorites'} on:click={(event) => toggleFavorite(event, workspace.activeProject!)}>{workspace.activeProject.favorite ? '★' : '☆'}</button></div><p>{workspace.view === 'timeline' ? 'Everything recently worked on in this board, in one chronological view.' : workspace.activeProject.description || 'A focused space for turning ideas into shipped work.'}</p></div>
    <div class="heading-actions"><button class="button quiet-button" type="button" data-report-bug-trigger on:click={openBugModal}><span aria-hidden="true">⚠</span> Report bug</button><button class="button primary" type="button" data-task-modal-trigger on:click={() => openTaskModal()}><span aria-hidden="true">＋</span> New task</button><input class="sr-only" bind:this={portable.portableFileInput} type="file" accept=".json,application/json" aria-label="Import a Helm portable archive" on:change={handlePortableFile} /></div>
  </section>


  {#if workspace.view === 'board'}
  <BoardToolbar />

  {#if board.boardFilterChips.length}<div class="board-filter-chips" aria-label="Active board filters">{#each board.boardFilterChips as chip (chip.id)}<button type="button" on:click={chip.clear} aria-label={`Remove ${chip.label} filter`}>{chip.label} <span aria-hidden="true">×</span></button>{/each}</div>{/if}
  {#if board.boardError}<div class="inline-alert error content-alert" role="alert"><span>!</span><span>{board.boardError}</span><button class="text-button" type="button" on:click={() => loadBoard()}>Retry</button></div>{/if}
  {#if board.boardOffline}<div class="inline-alert warning content-alert" role="status"><span aria-hidden="true">⌁</span><span>You are offline. Showing the last loaded board pages.</span><button class="text-button" type="button" on:click={() => loadBoard()}>Retry</button></div>{/if}
  {#if Object.values(board.boardPages).some((page) => Boolean(page.error))}<div class="inline-alert warning content-alert" role="status"><span aria-hidden="true">…</span><span>Some board columns could not be loaded.</span><button class="text-button" type="button" on:click={() => loadBoard()}>Retry columns</button></div>{/if}
  {#if board.boardReconciliationNotice}<div class="inline-alert info content-alert" role="status"><span aria-hidden="true">↻</span><span>{board.boardReconciliationNotice}</span><button class="text-button" type="button" on:click={() => loadBoard()}>Refresh</button></div>{/if}
  {#if board.boardLoading && board.tasks.length}<div class="board-loading-note" role="status" aria-live="polite">Loading the latest board pages…</div>{/if}
  <BoardColumns />
  {:else}
    <BoardTimeline
      items={timeline.boardTimelineItems}
      tasks={board.tasks}
      filter={timeline.boardTimelineFilter}
      loading={timeline.boardTimelineLoading}
      loadingOlder={timeline.boardTimelineLoadingOlder}
      error={timeline.boardTimelineError}
      hasOlder={Boolean(timeline.boardTimelineNextCursor)}
      onFilterChange={setBoardTimelineFilter}
      onLoadOlder={loadOlderBoardTimeline}
      onRetry={() => { if (workspace.activeProject) void loadBoardTimeline(workspace.activeProject.id, { reset: !timeline.boardTimelineItems.length }); }}
      onOpen={openBoardTimelineTask}
    />
  {/if}
{:else}
  <div class="empty-state welcome-state"><div class="welcome-orbit"><span>R</span></div><span class="eyebrow">Your workspace is ready</span><h1>Start with a project.</h1><p>Projects give your ideas a home. Create one, invite your agents, and keep the next step clear.</p><button class="button primary button-large" type="button" on:click={openProjectModal}>＋ Create your first project</button></div>
{/if}
