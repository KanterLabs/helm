<svelte:options runes={true} />

<script lang="ts">
  // Bulk change review dialog.
  import { bulkResultLabel, closeBulkModal, submitBulkChanges } from '../../bulk';
  import { focusTrap } from '../../dialogs';
  import { board } from '../../state/board.svelte';
  import { bulk } from '../../state/bulk.svelte';
</script>

{#if bulk.showBulkModal}
  <div class="modal-backdrop" role="presentation" on:click={closeBulkModal}></div>
  <div class="modal bulk-task-modal" role="dialog" aria-modal="true" aria-labelledby="bulk-task-modal-title" aria-describedby="bulk-task-modal-description" use:focusTrap>
    <div class="modal-header"><div><span class="eyebrow">{bulk.bulkReviewTasks.length} selected</span><h2 id="bulk-task-modal-title">Review bulk changes</h2></div><button class="icon-button" type="button" aria-label="Close bulk changes" on:click={closeBulkModal}>×</button></div>
    <p id="bulk-task-modal-description" class="bulk-modal-description">Choose one guarded change for every selected task. Each task keeps its own version and reports its own outcome.</p>
    {#if bulk.bulkError}<div class="inline-alert error" role="alert"><span>!</span>{bulk.bulkError}</div>{/if}
    <form on:submit|preventDefault={submitBulkChanges}>
      <fieldset class="bulk-mode-fieldset"><legend>Apply mode</legend><label class="check-label"><input type="radio" name="bulk-mode" value="partial" bind:group={bulk.bulkMode} /><span><strong>Partial</strong><small>Apply valid tasks and report conflicts individually.</small></span></label><label class="check-label"><input type="radio" name="bulk-mode" value="atomic" bind:group={bulk.bulkMode} /><span><strong>Atomic</strong><small>Apply all or roll back the whole selection.</small></span></label></fieldset>
      <label>Change<select aria-label="Bulk change" bind:value={bulk.bulkOperation}><option value="move">Move to a column</option><option value="assign">Assign to an actor</option><option value="priority">Set priority</option><option value="labels">Replace labels</option><option value="due_at">Set due date</option><option value="complete">Complete tasks</option><option value="block">Block tasks</option></select></label>
      {#if bulk.bulkOperation === 'move'}
        <label>Destination column<select aria-label="Bulk destination column" bind:value={bulk.bulkDestinationColumnId}>{#each board.sortedColumns.filter((column) => column.semantic_state === 'backlog' || column.semantic_state === 'ready') as column}<option value={column.id}>{column.name}</option>{/each}</select></label>
      {:else if bulk.bulkOperation === 'assign'}
        <label>Assignee <span class="optional">Optional · leave blank to unassign</span><input aria-label="Bulk assignee actor ID" bind:value={bulk.bulkAssignee} placeholder="Actor ID" /></label>
      {:else if bulk.bulkOperation === 'priority'}
        <label>Priority<select aria-label="Bulk priority" bind:value={bulk.bulkPriority}><option value="urgent">Urgent</option><option value="high">High</option><option value="normal">Normal</option><option value="low">Low</option></select></label>
      {:else if bulk.bulkOperation === 'labels'}
        <label>Labels <span class="optional">Comma separated · replaces current labels</span><input aria-label="Bulk labels" bind:value={bulk.bulkLabels} placeholder="frontend, release" /></label>
      {:else if bulk.bulkOperation === 'due_at'}
        <label>Due date <span class="optional">Optional · leave blank to clear</span><input aria-label="Bulk due date" type="date" bind:value={bulk.bulkDueDate} /></label>
      {:else if bulk.bulkOperation === 'complete'}
        <label>Completion note <span class="optional">Optional · added to activity</span><textarea aria-label="Bulk completion note" rows="2" bind:value={bulk.bulkReason} placeholder="Shipped in this release"></textarea></label>
      {:else if bulk.bulkOperation === 'block'}
        <label>Blocking reason<textarea aria-label="Bulk blocking reason" rows="2" bind:value={bulk.bulkReason} placeholder="Waiting on a decision" required></textarea></label>
      {/if}
      <section class="bulk-review-list" aria-labelledby="bulk-review-heading"><div class="section-heading-inline"><h3 id="bulk-review-heading">Tasks in this change</h3><span class="optional">{bulk.bulkReviewTasks.length} of 100 maximum</span></div>{#if bulk.bulkReviewTasks.length}<ul>{#each bulk.bulkReviewTasks as task (task.id)}<li><span class="task-key">{task.key}</span><span>{task.title}</span><span class="bulk-review-version">v{task.version}</span></li>{/each}</ul>{:else}<p class="bulk-empty-review">Applied tasks have been cleared from the selection. Close this review or select more tasks.</p>{/if}</section>
      {#if bulk.bulkResult}<section class="bulk-result" aria-labelledby="bulk-result-heading" role="status" aria-live="polite"><div class="section-heading-inline"><h3 id="bulk-result-heading">Result summary</h3><span>{bulk.bulkResult.applied} applied · {bulk.bulkResult.conflicts} conflicts · {bulk.bulkResult.skipped} skipped</span></div><ul>{#each bulk.bulkResult.results as result (result.reference)}<li><span class={`bulk-result-status ${result.status}`}>{bulkResultLabel(result.status)}</span><span class="task-key">{result.reference}</span>{#if result.error}<span class="bulk-result-error">{result.error.message}</span>{/if}</li>{/each}</ul></section>{/if}
      <div class="modal-actions"><button class="text-button" type="button" on:click={closeBulkModal}>Close</button><button class="button primary" type="submit" disabled={bulk.bulkSubmitting || !bulk.bulkReviewTasks.length}>{#if bulk.bulkSubmitting}<span class="button-spinner"></span>{/if}{bulk.bulkResult ? 'Retry remaining changes' : `Apply changes to ${bulk.bulkReviewTasks.length} tasks`}</button></div>
    </form>
  </div>
{/if}
