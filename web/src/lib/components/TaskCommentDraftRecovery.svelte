<script lang="ts">
  import type { TaskTimelineFilter } from '../types';
  import { filterLabel } from './taskActivityTimeline';
  export let body: string;
  export let saving = false;
  export let hiddenByFilter = false;
  export let filter: TaskTimelineFilter;
  export let error = '';
  export let guardMessage = '';
  export let onInput: (event: Event) => void;
  export let onSave: () => void;
  export let onDiscard: () => void;
  export let onShow: () => void;
</script>

<div class="task-timeline-draft-recovery" role="status" aria-live="polite">
  <div class="task-timeline-draft-recovery-heading">
    <div>
      <strong>Unsaved comment draft</strong>
      {#if hiddenByFilter}
        <p>This comment is hidden by the “{filterLabel(filter)}” filter. Your draft is still safe in this task.</p>
      {:else}
        <p>The comment is not in the current activity results. Your draft will stay here until you save or discard it.</p>
      {/if}
    </div>
    {#if hiddenByFilter}<button class="text-button" type="button" on:click={onShow}>Show comment</button>{/if}
  </div>
  <div class="task-timeline-comment-edit">
    <textarea rows="4" value={body} aria-label="Edit comment draft" disabled={saving} on:input={onInput}></textarea>
    <div class="task-timeline-comment-actions">
      <button class="button primary" type="button" disabled={!body.trim() || saving} on:click={onSave}>
        {#if saving}<span class="button-spinner"></span>{/if}Save
      </button>
      <button class="text-button" type="button" disabled={saving} on:click={onDiscard}>Discard draft</button>
    </div>
  </div>
  {#if error}<div class="task-timeline-comment-error" role="alert">{error}</div>{/if}
  {#if guardMessage}<div class="task-timeline-comment-error" role="alert">{guardMessage}</div>{/if}
</div>
