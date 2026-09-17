<script lang="ts">
  import { api } from '../api';
  import type { AgentNote, AgentNoteCategory, Task } from '../types';

  export let task: Task;
  export let currentActorId = '';
  export let canManage = false;

  const labels: Record<AgentNoteCategory, string> = {
    known_issue: 'Known issue', rejected_approach: 'Rejected approach', constraint: 'Constraint', workaround: 'Workaround'
  };
  const categories = Object.keys(labels) as AgentNoteCategory[];
  let notes: AgentNote[] = [];
  let loadedTask = '';
  let loading = false;
  let saving = false;
  let error = '';
  let composing = false;
  let category: AgentNoteCategory = 'known_issue';
  let body = '';
  let evidence = '';
  let editing = '';

  $: if (task?.id && task.id !== loadedTask) void load(task.id);

  function message(value: unknown): string {
    return value instanceof Error ? value.message : 'Agent notes could not be updated.';
  }
  function refs(value: string): string[] {
    return [...new Set(value.split(',').map((item) => item.trim()).filter(Boolean))];
  }
  function reset() {
    composing = false; editing = ''; category = 'known_issue'; body = ''; evidence = '';
  }
  async function load(taskId = task.id) {
    loadedTask = taskId; loading = true; error = '';
    try { notes = (await api.listAgentNotes(taskId)).data; }
    catch (value) { if (loadedTask === taskId) error = message(value); }
    finally { if (loadedTask === taskId) loading = false; }
  }
  function edit(note: AgentNote) {
    composing = true; editing = note.id; category = note.category; body = note.body; evidence = note.evidence.join(', ');
  }
  async function save() {
    if (!body.trim() || saving) return;
    saving = true; error = '';
    try {
      const input = { category, body: body.trim(), evidence: refs(evidence) };
      if (editing) {
        const prior = notes.find((note) => note.id === editing);
        if (!prior) return;
        const updated = await api.updateAgentNote(task.id, prior.id, prior.version, input);
        notes = notes.map((note) => note.id === updated.id ? updated : note);
      } else {
        notes = [...notes, await api.createAgentNote(task.id, input)];
      }
      reset();
    } catch (value) { error = message(value); }
    finally { saving = false; }
  }
  async function resolve(note: AgentNote) {
    if (saving) return;
    saving = true; error = '';
    try { await api.resolveAgentNote(task.id, note.id, note.version); notes = notes.filter((item) => item.id !== note.id); if (editing === note.id) reset(); }
    catch (value) { error = message(value); }
    finally { saving = false; }
  }
</script>

<section class="agent-notes" aria-labelledby="agent-notes-heading">
  <div class="heading">
    <div><h2 id="agent-notes-heading">Agent notes <span>{notes.length}/6</span></h2><p>Verified pitfalls and constraints for the next agent.</p></div>
    {#if !composing}<button class="text-button" type="button" disabled={notes.length >= 6 || loading} on:click={() => composing = true}>+ Add note</button>{/if}
  </div>
  {#if error}<div class="note-error" role="alert">{error} <button class="text-button" type="button" on:click={() => load()}>Retry</button></div>{/if}
  {#if loading}<p class="empty">Loading agent notes…</p>
  {:else if notes.length}
    <ul>{#each notes as note (note.id)}<li>
      <div class="note-top"><strong>{labels[note.category]}</strong><span>v{note.version}</span></div>
      <p>{note.body}</p>
      {#if note.evidence.length}<div class="evidence">Evidence: {note.evidence.join(' · ')}</div>{/if}
      {#if note.actor_id === currentActorId || canManage}<div class="actions"><button class="text-button" type="button" disabled={saving} on:click={() => edit(note)}>Edit</button><button class="text-button resolve" type="button" disabled={saving} on:click={() => resolve(note)}>Resolve</button></div>{/if}
    </li>{/each}</ul>
  {:else if !composing}<p class="empty">No active agent notes.</p>{/if}
  {#if composing}
    <form on:submit|preventDefault={save}>
      <label>Type<select bind:value={category}>{#each categories as value}<option value={value}>{labels[value]}</option>{/each}</select></label>
      <label>Reusable finding<textarea rows="3" maxlength="500" bind:value={body} placeholder="What was verified, and why does it matter?"></textarea></label>
      <label>Evidence <span>Optional, comma separated</span><input bind:value={evidence} placeholder="path/to/file, test name, issue ID" /></label>
      <div class="form-actions"><button class="text-button" type="button" on:click={reset}>Cancel</button><button class="button primary" type="submit" disabled={saving || !body.trim()}>{editing ? 'Update note' : 'Add note'}</button></div>
    </form>
  {/if}
</section>

<style>
  .agent-notes{border:1px solid var(--border);border-radius:12px;padding:16px;background:var(--surface-subtle)}
  .heading,.note-top,.actions,.form-actions{display:flex;align-items:center;justify-content:space-between;gap:12px}
  h2{font-size:14px;margin:0} h2 span,.heading p,.empty,label span,.evidence,.note-top span{color:var(--text-muted);font-size:12px}
  .heading p{margin:3px 0 0}.text-button{white-space:nowrap}ul{display:grid;gap:8px;list-style:none;margin:12px 0 0;padding:0}
  li{border:1px solid var(--border);border-radius:9px;background:var(--surface);padding:11px}li p{font-size:13px;line-height:1.45;margin:7px 0;white-space:pre-wrap}
  .note-top strong{font-size:11px;letter-spacing:.04em;text-transform:uppercase}.evidence{overflow-wrap:anywhere}.actions{justify-content:flex-end;margin-top:7px}.resolve{color:var(--danger)}
  form{display:grid;gap:10px;margin-top:12px;padding-top:12px;border-top:1px solid var(--border)}label{display:grid;gap:5px;font-size:12px;font-weight:600}select,input,textarea{width:100%;box-sizing:border-box}.form-actions{justify-content:flex-end}.note-error{margin-top:10px;color:var(--danger);font-size:12px}.empty{margin:12px 0 0}
</style>
