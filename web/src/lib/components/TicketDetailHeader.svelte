<script lang="ts">
  import { api } from '../api';
  import { actorId } from '../state';
  import type { Actor, Project, Task } from '../types';
  import { ticketAssigneeLabel, ticketPriorities, ticketPriorityLabels, ticketStatus, ticketStatusLabels } from '../tickets';

  export let detail: Task;
  export let user: Actor;
  export let actorNames: Record<string, string> = {};
  export let workProjects: Project[] = [];
  /** True once the ticket is filed into a work project. */
  export let filed = false;
  export let fileProject = '';
  export let actionPending = false;
  export let heading: HTMLElement | null = null;
  export let onAct: (label: string, run: (task: Task) => Promise<Task>) => void | Promise<void> = () => undefined;
  export let onFile: (projectId: string) => void | Promise<void> = () => undefined;
</script>

<div class="ticket-detail-header">
  <div class="ticket-row-top"><span class="task-key">{detail.key}</span><span class={`ticket-status status-${ticketStatus(detail)}`} data-ticket-status>{ticketStatusLabels[ticketStatus(detail)]}</span>{#if detail.ticket?.origin === 'alert'}<span class="ticket-origin">Alert</span>{/if}</div>
  <h2 id="ticket-detail-title" tabindex="-1" bind:this={heading}>{detail.title}</h2>
  <div class="ticket-controls">
    <label>Priority<select aria-label="Ticket priority" value={detail.priority} disabled={actionPending} on:change={(event) => { const priority = event.currentTarget.value as Task['priority']; void onAct('Priority', (task) => api.patchTask(task.id, { priority }, task.version)); }}>{#each ticketPriorities as value}<option value={value}>{ticketPriorityLabels[value]}</option>{/each}</select></label>
    <label>Project<select aria-label="File to project" bind:value={fileProject} disabled={actionPending} on:change={() => onFile(fileProject)}>{#if !filed}<option value="">Not filed</option>{/if}{#each workProjects as project (project.id)}<option value={project.id}>{project.key} · {project.name}</option>{/each}</select></label>
    <span class="ticket-assignee">Assignee: <strong>{ticketAssigneeLabel(detail, user.id, actorNames)}</strong></span>
    {#if actorId(detail.assignee) !== user.id}<button class="button quiet-button compact" type="button" disabled={actionPending} on:click={() => onAct('Assignment', (task) => api.patchTask(task.id, { assignee: user.id }, task.version))}>Assign to me</button>{/if}
    {#if actorId(detail.assignee)}<button class="text-button" type="button" disabled={actionPending} on:click={() => onAct('Assignment', (task) => api.patchTask(task.id, { assignee: null }, task.version))}>Unassign</button>{/if}
  </div>
</div>

<style>
  .ticket-row-top { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
  .ticket-status { padding: 2px 7px; border: 1px solid var(--border-strong); border-radius: 5px; font-size: var(--text-xs); font-weight: 600; }
  .ticket-status.status-needs_triage { color: var(--semantic-amber); background: var(--amber-soft); }
  .ticket-status.status-waiting { color: var(--semantic-red); background: var(--red-soft); }
  .ticket-status.status-in_progress { color: var(--purple); background: var(--purple-soft); }
  .ticket-status.status-completed { color: var(--semantic-green); background: var(--green-soft); }
  .ticket-origin { padding: 2px 6px; border-radius: 5px; color: var(--ink-soft); background: var(--surface-muted); font-size: var(--text-xs); font-weight: 600; }
  .ticket-detail-header h2 { margin: 6px 0 0; font: 600 var(--text-lg) var(--font-sans); letter-spacing: -.02em; }
  .ticket-detail-header h2:focus-visible { outline: 2px solid var(--purple); outline-offset: 2px; }
  .ticket-controls { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; margin-top: 10px; font-size: var(--text-xs); }
  .ticket-controls label { display: inline-flex; align-items: center; gap: 6px; font-weight: 600; }
  .compact { min-height: 30px; }
</style>
