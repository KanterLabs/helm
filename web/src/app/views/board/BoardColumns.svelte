<svelte:options runes={true} />

<script lang="ts">
  import { isBugIntake } from '../../../lib/bugIntake';
  import { openBugModal } from '../../bugModal';
  // Board columns, cards, drag and drop and quick add.
  import { boardCardHeight } from '../../../lib/boardLayout';
  import { makeBoardOrderingGate } from '../../../lib/boardOrdering';
  import AgentPulse from '../../../lib/components/AgentPulse.svelte';
  import BoardCardActions from '../../../lib/components/BoardCardActions.svelte';
  import BoardOverflowNavigation from '../../../lib/components/BoardOverflowNavigation.svelte';
  import TaskDependencyStatus from '../../../lib/components/TaskDependencyStatus.svelte';
  import { dependencyBlocked } from '../../../lib/dependencies';
  import { hierarchyBadgeLabel } from '../../../lib/hierarchy';
  import { actorId, actorName, formatDate } from '../../../lib/state';
  import { boardCardActions, cancelQuickAdd, columnColor, dragLeaveColumn, dragOverColumn, dragStart, dropTask, endDrag, openQuickAdd, showBoardCardPage, submitQuickAdd } from '../../board/cards';
  import { loadBoard, loadBoardColumn, loadMoreBoardColumn } from '../../board/load';
  import { keyboardMove } from '../../board/ordering';
  import { boardFiltersActive, clearFilters, columnScroll, toggleBoardTaskExpanded } from '../../board/pages';
  import { toggleTaskSelection } from '../../bulk';
  import { boardRenderLimit, priorityLabels } from '../../constants';
  import { openTask } from '../../drawer/open';
  import { board } from '../../state/board.svelte';
  import { bulk } from '../../state/bulk.svelte';
  import { creation } from '../../state/creation.svelte';
  import { drawer } from '../../state/drawer.svelte';
  import { live } from '../../state/live.svelte';
  import { workspace } from '../../state/workspace.svelte';
  import { agentLabelForTask, showAgentPulse, taskDueClass, taskReleaseName } from '../../taskQueries';

  // Rendered only while workspace.activeProject is set (guarded by the parent).
  const boardProject = $derived(workspace.activeProject!);
</script>

{#if board.boardLoading && !board.tasks.length}
  <div class="board board-loading" role="status" aria-label="Loading board" aria-live="polite" aria-busy="true"><span class="sr-only">Loading board…</span>{#each [1, 2, 3, 4] as item}<div class="column-skeleton" aria-hidden="true"><div></div><div></div><div></div></div>{/each}</div>
{:else if !board.sortedColumns.length}
  <div class="empty-state board-empty"><div class="empty-icon">◇</div><h2>Your board is almost ready</h2><p>Columns will appear here once this project has been initialized.</p><button class="button primary" type="button" on:click={() => loadBoard()}>Refresh board</button></div>
{:else}
  <BoardOverflowNavigation label={`${boardProject.name} board columns`}>
  <section class="board" data-board-overflow-scroll use:boardCardHeight aria-label={`${boardProject.name} board`}>
    {#each board.sortedColumns as column (column.id)}
    {@const orderingGate = makeBoardOrderingGate({
      criteriaTransition: board.boardCriteriaTransition,
      filterTimerPending: board.boardFilterTimer !== undefined,
      boardLoading: board.boardLoading,
      metadataError: Boolean(board.boardError),
      pageRefreshActive: Boolean(board.boardColumnRefreshes[column.id]),
      page: board.boardPages[column.id],
      filters: board.filters,
      workFilter: board.boardWorkFilter,
      sort: board.boardSort,
      order: board.boardOrder
    })}
    {@const orderedColumnTasks = board.tasksByColumn[column.id] || []}
    {@const cardOffset = Math.min(board.boardCardOffsets[column.id] || 0, Math.floor(Math.max(0, orderedColumnTasks.length - 1) / boardRenderLimit) * boardRenderLimit)}
    <article
      class="board-column"
      class:bug-intake={isBugIntake(column)}
      class:drop-target={board.dragOverColumnId === column.id}
      aria-label={`${column.name} column`}
      aria-dropeffect="move"
      on:dragover={(event) => dragOverColumn(event, column.id)}
      on:dragleave={(event) => dragLeaveColumn(event, column.id)}
      on:drop={(event) => dropTask(event, column.id)}
    >
        <header class="column-header"><div class="column-name"><span class="column-dot" style={`--column-color: ${columnColor(column)}`}></span><h2>{column.name}</h2><span class="column-count">{board.tasksByColumn[column.id].length}{board.boardPages[column.id]?.nextCursor ? '+' : ''}</span></div></header>
        {#if isBugIntake(column)}<p class="bug-intake-hint">Review reports, then triage into Ready</p>{/if}
        <div class="column-progress"><span style={`width: ${Math.min(100, board.tasksByColumn[column.id].length * 4)}%; --column-color: ${columnColor(column)}`}></span></div>
        <div class="column-cards" use:columnScroll={{ scope: `${boardProject.id}:${board.boardCriteriaRevision}`, column: column.id, page: cardOffset, ready: !board.boardLoading }}>
          {#if board.dragOverColumnId === column.id && board.draggingTaskId && board.tasksByColumn[column.id].some((task) => task.id === board.draggingTaskId) === false}
            <div class="drop-placeholder" role="status">Drop task in {column.name}</div>
          {/if}
          {#if !board.tasksByColumn[column.id].length}
            <div class="column-empty">{#if board.boardPages[column.id]?.error}<span>{board.boardPages[column.id].error}</span><button class="text-button" type="button" on:click={() => loadBoardColumn(column.id, { reset: true })}>Retry</button>{:else if boardFiltersActive()}<span>No tasks match the current filters.</span><button class="text-button" type="button" on:click={clearFilters}>Clear filters</button>{:else}<span>No tasks yet</span>{/if}</div>
          {:else}
            {#each orderedColumnTasks.slice(cardOffset, cardOffset + boardRenderLimit) as task (task.id)}
              <article class="task-card" class:expanded={board.expandedBoardTaskIds.has(task.id)} class:dependency-blocked={dependencyBlocked(task)} class:dragging={board.draggingTaskId === task.id} on:dragend={endDrag} on:dragover|preventDefault={(event) => { if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'; }} on:drop={(event) => dropTask(event, column.id, task.id)}>
                <label class="task-select">
                  <span class="sr-only">Select {task.key}</span>
                  <input type="checkbox" aria-label={`Select ${task.key}`} checked={bulk.selectedTaskIds.has(task.id)} on:click|stopPropagation on:change={() => toggleTaskSelection(task)} />
                  <span class="task-select-box" aria-hidden="true"></span>
                </label>
                <button class="task-drag-handle" type="button" draggable="true" aria-label={`Drag ${task.key}, ${task.title}`} title="Drag task" on:click|stopPropagation={() => undefined} on:dragstart|stopPropagation={(event) => dragStart(event, task)}>⠿</button>
                <button class="task-main" type="button" data-task-trigger={task.id} aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown Alt+Home Alt+End" on:click={() => openTask(task)} on:keydown={(event) => keyboardMove(event, task)}>
                  <span class="task-card-top"><span class="task-key">{task.key}</span>{#if task.kind === 'bug'}<span class="task-bug-meta"><span class="issue-kind-badge">Bug</span>{#if task.bug?.severity}<span class="severity-badge">{task.bug.severity.toUpperCase()}</span>{:else}<span class="severity-badge untriaged">Needs triage</span>{/if}</span>{/if}<span class={`priority-dot priority-${task.priority}`} title={`${priorityLabels[task.priority]} priority`}></span>{#if taskReleaseName(task)}<span class="release-chip" title={`Target focus: ${taskReleaseName(task)}`}>◈ {taskReleaseName(task)}</span>{/if}{#if task.claimed_by}<span class="claim-mini" title={`Claimed by ${actorName(task.claimed_by) || 'another actor'}`}>●</span>{/if}</span>
                  <strong class="task-title">{task.title}</strong>
                  {#if task.description}<span class="task-excerpt">{task.description.replace(/[#*_`]/g, '').slice(0, 92)}{task.description.length > 92 ? '…' : ''}</span>{/if}
	                              {#if task.labels?.length}<span class="task-labels">{#each task.labels.slice(0, 3) as label}<span class="label-chip" style={`--label-color: ${label.color || '#8b7cf6'}`}>{label.name}</span>{/each}{#if task.labels.length > 3}<span class="label-more">+{task.labels.length - 3}</span>{/if}</span>{/if}
	                              <TaskDependencyStatus {task} />
	                              {#if task.checklist_summary?.total}<span class="checklist-card-progress" title={`${task.checklist_summary.completed} of ${task.checklist_summary.total} checklist items complete`} aria-label={`${task.checklist_summary.completed} of ${task.checklist_summary.total} checklist items complete`}>☑ {task.checklist_summary.completed}/{task.checklist_summary.total}</span>{/if}
	                              {#if hierarchyBadgeLabel(task)}<span class="hierarchy-badge" aria-label={`Hierarchy: ${hierarchyBadgeLabel(task)}`}><span aria-hidden="true">⌘</span>{hierarchyBadgeLabel(task)}</span>{/if}
                </button>
                {#if showAgentPulse(task)}<AgentPulse {task} now={live.pulseClock} actorLabel={agentLabelForTask(task)} compact />{/if}
                <div class="task-card-footer"><button class="card-expand-toggle" type="button" aria-expanded={board.expandedBoardTaskIds.has(task.id)} aria-label={`${board.expandedBoardTaskIds.has(task.id) ? 'Hide' : 'Show'} details for ${task.key}`} on:click|stopPropagation={() => toggleBoardTaskExpanded(task.id)}><span class="card-expand-icon" aria-hidden="true"></span>Details</button><span class={`due-date ${taskDueClass(task)}`}>{#if task.due_at}<span aria-hidden="true">◷</span>{formatDate(task.due_at)}{/if}</span><span class="card-footer-spacer"></span>{#if task.assignee}<span class="mini-avatar" title={`Assigned to ${actorName(task.assignee) || actorId(task.assignee)}`}>{(actorName(task.assignee) || actorId(task.assignee)).slice(0, 1).toUpperCase()}</span>{/if}{#if task.comment_count}<span class="comment-count" title={`${task.comment_count} comments`}>◌ {task.comment_count}</span>{/if}<BoardCardActions taskKey={task.key} actions={boardCardActions(task, orderedColumnTasks, orderingGate, drawer.taskActionLoading === task.id)} /></div>
              </article>
            {/each}
          {/if}
          {#if orderedColumnTasks.length > boardRenderLimit}<div class="column-empty" role="status">Showing {cardOffset + 1}–{Math.min(cardOffset + boardRenderLimit, orderedColumnTasks.length)} of {orderedColumnTasks.length} loaded cards</div>{/if}
          {#if cardOffset > 0}<button class="load-more-tasks" type="button" on:click={(event) => showBoardCardPage(event, column.id, cardOffset - boardRenderLimit)}>Show previous cards</button>{/if}
          {#if cardOffset + boardRenderLimit < orderedColumnTasks.length}<button class="load-more-tasks" type="button" on:click={(event) => showBoardCardPage(event, column.id, cardOffset + boardRenderLimit)}>Show next cards</button>
          {:else if board.boardPages[column.id]?.nextCursor}<button class="load-more-tasks" type="button" on:click={() => loadMoreBoardColumn(column.id)} disabled={board.boardPages[column.id].loading}>{board.boardPages[column.id].loading ? 'Loading…' : 'Load more tasks'}</button>{/if}
          {#if board.boardPages[column.id]?.error && board.tasksByColumn[column.id].length}<div class="column-page-error" role="alert"><span>{board.boardPages[column.id].error}</span><button class="text-button" type="button" on:click={() => loadBoardColumn(column.id, { reset: false })}>Retry</button></div>{/if}
        </div>
        <div class="quick-add-wrap">
          {#if isBugIntake(column)}<button class="quick-add-trigger" type="button" on:click={() => openBugModal()}><span>＋</span> Report bug</button>
          {:else if board.quickAddColumn === column.id}
            <form class="quick-add-form" aria-busy={creation.quickAddCreating} on:submit|preventDefault={() => submitQuickAdd(column.id)}><input bind:this={board.quickAddInput} bind:value={() => board.quickAddTitle[column.id], (value) => board.quickAddTitle = { ...board.quickAddTitle, [column.id]: value }} disabled={creation.quickAddCreating || Boolean(creation.quickCreation)} aria-label={`New task in ${column.name}`} placeholder="What needs doing?" />{#if creation.quickAddError}<p role="alert">{creation.quickAddError}</p>{/if}{#if creation.quickAddCreating}<p role="status">Saving…</p>{/if}<div><button class="text-button" type="button" disabled={creation.quickAddCreating || Boolean(creation.quickCreation)} on:click={cancelQuickAdd}>Cancel</button><button class="button primary compact-button" type="submit" disabled={creation.quickAddCreating || (!creation.quickCreation && !board.quickAddTitle[column.id]?.trim())}>{creation.quickAddCreating ? 'Saving…' : creation.quickCreation ? 'Retry original submission' : 'Add task'}</button></div></form>
          {:else}<button class="quick-add-trigger" type="button" data-quick-add-trigger={column.id} on:click={(event) => openQuickAdd(column.id, event.currentTarget as HTMLButtonElement)}><span>＋</span> Add task</button>{/if}
        </div>
      </article>
    {/each}
  </section>
  </BoardOverflowNavigation>
{/if}

<style>
  .bug-intake { border-top: 2px solid var(--semantic-amber); }
  .bug-intake-hint { margin: -2px 14px 8px; color: var(--muted); font-size: var(--text-xs); }
</style>
