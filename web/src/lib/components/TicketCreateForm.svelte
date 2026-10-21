<script lang="ts" context="module">
  import type { Task } from '../types';

  /** Unsent ticket fields; the parent keeps them so Cancel does not discard a draft. */
  export type TicketDraft = { title: string; description: string; priority: Task['priority']; assignMe: boolean };

  export function emptyTicketDraft(): TicketDraft {
    return { title: '', description: '', priority: 'normal', assignMe: true };
  }
</script>

<script lang="ts">
  import { api } from '../api';
  import type { Actor } from '../types';
  import { ticketErrorMessage, ticketPriorities, ticketPriorityLabels } from '../tickets';

  export let user: Actor;
  export let draft: TicketDraft = emptyTicketDraft();
  export let onCreated: (task: Task) => void | Promise<void> = () => undefined;
  export let onCancel: () => void = () => undefined;

  let createError = '';
  let createSaving = false;

  async function createTicket() {
    if (!draft.title.trim() || createSaving) return;
    createSaving = true;
    createError = '';
    try {
      const task = await api.createTicket({
        title: draft.title.trim(),
        description: draft.description.trim() || undefined,
        priority: draft.priority,
        assignee: draft.assignMe ? user.id : null
      });
      draft = { ...draft, title: '', description: '', priority: 'normal' };
      await onCreated(task);
    } catch (error) {
      createError = ticketErrorMessage(error, 'The ticket could not be created.');
    } finally {
      createSaving = false;
    }
  }
</script>

<form class="ticket-create" aria-label="New ticket" on:submit|preventDefault={createTicket}>
  <label>Title<input bind:value={draft.title} maxlength="500" required placeholder="What needs attention?" /></label>
  <label>Details <span class="optional">Optional</span><textarea rows="3" bind:value={draft.description} placeholder="Context, links, or the alert text"></textarea></label>
  <div class="ticket-create-row">
    <label>Priority<select bind:value={draft.priority}>{#each ticketPriorities as value}<option value={value}>{ticketPriorityLabels[value]}</option>{/each}</select></label>
    <label class="ticket-check"><input type="checkbox" bind:checked={draft.assignMe} /> Assign to me</label>
  </div>
  {#if createError}<div class="inline-alert error" role="alert"><span>!</span>{createError}</div>{/if}
  <div class="ticket-create-actions"><button class="text-button" type="button" on:click={onCancel}>Cancel</button><button class="button primary" type="submit" disabled={!draft.title.trim() || createSaving}>{createSaving ? 'Creating…' : 'Create ticket'}</button></div>
</form>

<style>
  .ticket-create { display: grid; gap: 10px; padding: 14px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface); }
  .ticket-create label { display: grid; gap: 5px; font-size: var(--text-xs); font-weight: 600; }
  .ticket-create-row { display: flex; flex-wrap: wrap; align-items: end; gap: 14px; }
  .ticket-check { display: flex !important; align-items: center; gap: 6px !important; }
  .ticket-create-actions { display: flex; justify-content: flex-end; align-items: center; gap: 12px; }
  .optional { color: var(--muted); font-size: var(--text-xs); }
</style>
