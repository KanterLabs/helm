<script lang="ts">
  import { api } from '../api';
  import { renderMarkdown } from '../markdown';
  import type { Comment } from '../types';
  import { ticketAge, ticketErrorMessage } from '../tickets';

  export let taskId: string;
  export let comments: Comment[] = [];
  export let noteDraft = '';
  export let userId = '';
  export let actorNames: Record<string, string> = {};
  export let onError: (message: string) => void = () => undefined;

  let notePosting = false;

  async function postNote() {
    if (!noteDraft.trim() || notePosting) return;
    // A response for a ticket the user has since left must not land here.
    const requestTaskId = taskId;
    notePosting = true;
    try {
      const comment = await api.postComment(requestTaskId, noteDraft.trim());
      if (requestTaskId !== taskId) return;
      comments = [...comments, comment];
      noteDraft = '';
    } catch (error) {
      if (requestTaskId === taskId) onError(ticketErrorMessage(error, 'The note could not be saved.'));
    } finally {
      if (requestTaskId === taskId) notePosting = false;
    }
  }
</script>

<section class="drawer-section ticket-notes" aria-labelledby="ticket-notes-heading">
  <h3 id="ticket-notes-heading">Notes</h3>
  {#if comments.length}
    <ul class="ticket-note-list">
      {#each comments as comment (comment.id)}
        <li><span class="ticket-note-author">{comment.actor_id === userId ? 'You' : actorNames[comment.actor_id] || 'Teammate'} · {ticketAge(comment.created_at)}</span><div>{@html renderMarkdown(comment.body)}</div></li>
      {/each}
    </ul>
  {:else}<p class="optional">No notes yet.</p>{/if}
  <form class="ticket-note-form" on:submit|preventDefault={postNote}>
    <label><span class="sr-only">Add internal note</span><textarea rows="2" bind:value={noteDraft} placeholder="Add an internal note (visible to everyone on this project)"></textarea></label>
    <button class="button quiet-button" type="submit" disabled={!noteDraft.trim() || notePosting}>{notePosting ? 'Saving…' : 'Add note'}</button>
  </form>
</section>

<style>
  .ticket-notes { margin: 0; }
  .ticket-notes h3 { margin: 0 0 8px; font-size: var(--text-sm); }
  .ticket-note-list { display: grid; gap: 10px; margin: 0 0 10px; padding: 0; list-style: none; font-size: var(--text-sm); }
  .ticket-note-author { color: var(--muted); font-size: var(--text-xs); font-weight: 600; }
  .ticket-note-form { display: grid; gap: 8px; justify-items: end; }
  .ticket-note-form label, .ticket-note-form textarea { width: 100%; }
  .optional { color: var(--muted); font-size: var(--text-xs); }
</style>
