<svelte:options runes={true} />

<script lang="ts">
  // Project focus areas and their work queues.
  import { formatDate } from '../../../lib/state';
  import { completeRelease, deleteRelease, openReleaseModal, openReleaseReopen } from '../../releases/edit';
  import { loadReleaseQueue, loadReleasesPage, openReleaseFilteredBoard, openWorkTaskById, releaseProgress, releaseStatusLabel, releaseSummary, releaseWritesDisabled, selectRelease } from '../../releases/load';
  import { setView } from '../../routing';
  import { releasePlan } from '../../state/releasePlan.svelte';
  import { workspace } from '../../state/workspace.svelte';
</script>

{#if workspace.activeProject}
  <section class="page-heading release-page-heading">
    <div><div class="breadcrumbs"><span>Workspace</span><span>/</span><span>{workspace.activeProject.key}</span><span>/</span><span>Focus</span></div><h1>{workspace.activeProject.name} focus</h1><p>Group the tasks that matter now, track readiness, and keep the goal clear.</p></div>
    <div class="heading-actions"><button class="button quiet-button" type="button" on:click={() => void loadReleasesPage()} disabled={releasePlan.releasesLoading}>↻ Refresh</button><button class="button primary" type="button" data-release-modal-trigger on:click={() => openReleaseModal()} disabled={releaseWritesDisabled()}>＋ New focus</button></div>
  </section>
  {#if releaseWritesDisabled()}<div class="inline-alert warning content-alert" role="status"><span aria-hidden="true">⌁</span><span>Focus planning is read-only while offline. Existing focus references remain visible.</span></div>{/if}
  {#if releasePlan.releasesError}<div class="inline-alert error content-alert" role="alert"><span>!</span><span>{releasePlan.releasesError}</span><button class="text-button" type="button" on:click={() => void loadReleasesPage()}>Retry</button></div>{/if}
  {#if releasePlan.releasesLoading && !releasePlan.activeProjectReleases.length}
    <div class="release-skeleton" role="status" aria-label="Loading focus areas"><div></div><div></div><div></div></div>
  {:else}
    <section class="release-layout" aria-label={`${workspace.activeProject.name} focus planning`}>
      <section class="release-list-panel" aria-labelledby="release-list-heading">
        <div class="release-panel-heading"><div><span class="eyebrow">Focus</span><h2 id="release-list-heading">Active and completed</h2><p>Active focus appears first by target date; completed history stays available below.</p></div><span class="release-count">{releasePlan.sortedReleases.length}</span></div>
        {#if releasePlan.sortedReleases.length}
          <div class="release-list" role="list">
            {#each releasePlan.sortedReleases as release (release.id)}
              {@const summary = releaseSummary(release)}
              {@const progress = releaseProgress(release)}
              <article class="release-row" class:selected={releasePlan.releaseSelectedId === release.id} data-release-id={release.id} role="listitem">
                <button class="release-row-select" type="button" aria-pressed={releasePlan.releaseSelectedId === release.id} on:click={() => selectRelease(release)}>
                  <span class="release-row-heading"><strong>{release.name}</strong><span class:released={release.status === 'released'} class="release-status">{releaseStatusLabel(release)}</span></span>
                  <span class="release-row-meta">{release.target_date ? `Target ${formatDate(release.target_date)}` : 'No target date'}{#if release.status === 'released' && release.released_at} · Completed {formatDate(release.released_at)}{/if}</span>
                  <span class="release-progress-label"><span>{summary.completed_count ?? 0} of {summary.task_count ?? 0} direct tasks complete</span><strong>{progress}%</strong></span>
                  <span class="release-progress-track" aria-label={`${progress}% complete`}><span style={`width: ${progress}%`}></span></span>
                  <span class="release-row-signals"><span>{summary.required_completed_count ?? 0}/{summary.required_task_count ?? 0} required</span>{#if summary.blocked_count}<span class="signal-blocked">{summary.blocked_count} blocked</span>{/if}{#if summary.claimed_count}<span>{summary.claimed_count} claimed</span>{/if}{#if summary.cross_release_conflict_count}<span class="signal-conflict">{summary.cross_release_conflict_count} cross-focus conflicts</span>{/if}</span>
                </button>
                <div class="release-row-actions">{#if release.status === 'planned'}<button class="button quiet-button compact-button" type="button" disabled={releaseWritesDisabled()} on:click={() => openReleaseModal(release)}>Edit focus</button><button class="button quiet-button compact-button" type="button" disabled={releaseWritesDisabled()} on:click={() => void completeRelease(release)}>Complete focus</button><button class="icon-button tiny danger-button" type="button" aria-label={`Delete focus ${release.name}`} disabled={releaseWritesDisabled()} on:click={() => void deleteRelease(release)}>×</button>{:else}<button class="button quiet-button compact-button" type="button" disabled={releaseWritesDisabled()} on:click={() => openReleaseReopen(release)}>Reopen focus</button>{/if}</div>
              </article>
            {/each}
          </div>
        {:else}
          <div class="empty-state compact-empty release-empty"><div class="empty-icon">◈</div><h3>No focus yet</h3><p>Create a focus to group the tasks that matter now and track readiness.</p><button class="button primary" type="button" data-release-modal-trigger on:click={() => openReleaseModal()} disabled={releaseWritesDisabled()}>＋ Create a focus</button></div>
        {/if}
      </section>
      <aside class="release-detail-panel" aria-labelledby="release-detail-heading">
        {#if releasePlan.selectedRelease}
          {@const detailSummary = releaseSummary(releasePlan.selectedRelease)}
          {@const detailProgress = releaseProgress(releasePlan.selectedRelease)}
          <div class="release-detail-heading"><div><span class="eyebrow">Focus plan</span><h2 id="release-detail-heading">{releasePlan.selectedRelease.name}</h2><span class:released={releasePlan.selectedRelease.status === 'released'} class="release-status">{releaseStatusLabel(releasePlan.selectedRelease)}</span></div><button class="button quiet-button compact-button" type="button" on:click={() => openReleaseFilteredBoard(releasePlan.selectedRelease!)}>Open filtered board</button></div>
          {#if releasePlan.selectedRelease.description}<p class="release-description">{releasePlan.selectedRelease.description}</p>{/if}
          <div class="release-detail-target">{releasePlan.selectedRelease.target_date ? `Target date · ${formatDate(releasePlan.selectedRelease.target_date)}` : 'No target date set'}{#if releasePlan.selectedRelease.status === 'released' && releasePlan.selectedRelease.released_at}<span>Completed {formatDate(releasePlan.selectedRelease.released_at)}</span>{/if}</div>
          <div class="release-summary-grid"><article><span>Direct progress</span><strong>{detailProgress}%</strong><small>{detailSummary.completed_count ?? 0}/{detailSummary.task_count ?? 0} complete</small></article><article><span>Required work</span><strong>{releaseProgress(releasePlan.selectedRelease, true)}%</strong><small>{detailSummary.required_completed_count ?? 0}/{detailSummary.required_task_count ?? 0} complete</small></article><article><span>Readiness</span><strong class:ready={detailSummary.ready_to_release} class="release-readiness">{releasePlan.selectedRelease.status === 'released' ? 'Completed' : detailSummary.ready_to_release ? 'Ready' : 'Not ready'}</strong><small>{detailSummary.blocked_count ?? 0} blocked · {detailSummary.claimed_count ?? 0} claimed</small></article></div>
          {#if detailSummary.cross_release_conflict_count || detailSummary.checklist_warning_count}<div class="inline-alert warning release-warning" role="status"><span>!</span><span>{detailSummary.cross_release_conflict_count ?? 0} cross-focus conflicts · {detailSummary.checklist_warning_count ?? 0} checklist warnings need review.</span></div>{/if}
          <section class="release-queue-section" aria-labelledby="release-queue-heading"><div class="release-section-heading"><div><h3 id="release-queue-heading">Work queue</h3><p>Direct members and prerequisites that affect readiness.</p></div><button class="icon-button" type="button" aria-label="Refresh focus work queue" on:click={() => void loadReleaseQueue(releasePlan.selectedRelease!.id)}>↻</button></div>{#if releasePlan.releaseQueueError}<div class="inline-alert error" role="alert"><span>!</span>{releasePlan.releaseQueueError}</div>{/if}{#if releasePlan.releaseQueueLoading}<div class="release-queue-loading" role="status"><span class="spinner"></span>Loading queue…</div>{:else if releasePlan.releaseQueue?.data?.length}<ul class="release-queue-list">{#each releasePlan.releaseQueue.data as item (item.task?.id)}<li><button type="button" class="release-queue-task" on:click={() => item.task?.id && void openWorkTaskById(item.task.id)}><span class="task-key">{item.task?.key || 'Task'}</span><span>{item.relationship === 'prerequisite' ? 'Prerequisite' : 'Direct member'}</span></button><span class={`queue-disposition ${item.disposition}`}>{item.disposition?.split('_').join(' ')}</span>{#if item.blocked_by?.length}<small>Blocked by {item.blocked_by.map((blocker) => blocker.key).join(', ')}</small>{/if}</li>{/each}</ul>{:else}<p class="release-queue-empty">No open work is blocking this focus. The queue will refresh when assignments or dependencies change.</p>{/if}</section>
        {:else}
          <div class="empty-state compact-empty release-detail-empty"><div class="empty-icon">◈</div><h3>Select a focus</h3><p>Choose an active focus to inspect progress, blockers, and claimable work.</p></div>
        {/if}
      </aside>
    </section>
  {/if}
{:else}
  <div class="empty-state welcome-state"><div class="welcome-orbit"><span>R</span></div><span class="eyebrow">Choose a project</span><h1>Focus areas live with a project.</h1><p>Select a project first, then group the tasks that matter now.</p><button class="button primary button-large" type="button" on:click={() => setView('board')}>Browse projects</button></div>
{/if}
