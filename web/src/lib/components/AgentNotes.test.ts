// @vitest-environment jsdom
import { mount, tick, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../api';
import type { AgentNote, Task } from '../types';
import AgentNotes from './AgentNotes.svelte';

const mounted: Array<ReturnType<typeof mount>> = [];
afterEach(async () => { while (mounted.length) await unmount(mounted.pop()!); document.body.replaceChildren(); vi.restoreAllMocks(); });

const task: Task = { id:'task-1', number:1, key:'TC-1', project_id:'project-1', column_id:'column-1', title:'Notes', priority:'normal', position:1, version:1 };
function note(id = 'note-1'): AgentNote { return { id, task_id:task.id, actor_id:'agent-1', category:'known_issue', body:'Do not retry the broken path.', evidence:['internal/store'], version:1, created_at:'2026-01-01T00:00:00Z', updated_at:'2026-01-01T00:00:00Z' }; }

describe('AgentNotes', () => {
  it('loads, creates, and resolves curated notes', async () => {
    vi.spyOn(api, 'listAgentNotes').mockResolvedValue({ data:[note()], next_cursor:'' });
    vi.spyOn(api, 'createAgentNote').mockResolvedValue(note('note-2'));
    vi.spyOn(api, 'resolveAgentNote').mockResolvedValue(undefined);
    mounted.push(mount(AgentNotes, { target:document.body, props:{ task, currentActorId:'agent-1' } }));
    await vi.waitFor(() => expect(document.body.textContent).toContain('Do not retry the broken path.'));
    document.querySelector<HTMLButtonElement>('.heading .text-button')!.click(); await tick();
    const textarea = document.querySelector<HTMLTextAreaElement>('textarea')!;
    textarea.value = 'A second verified issue.'; textarea.dispatchEvent(new Event('input', { bubbles:true }));
    document.querySelector<HTMLFormElement>('form')!.dispatchEvent(new Event('submit', { bubbles:true, cancelable:true }));
    await vi.waitFor(() => expect(api.createAgentNote).toHaveBeenCalledWith('task-1', expect.objectContaining({ body:'A second verified issue.' })));
    await vi.waitFor(() => expect(document.querySelector<HTMLButtonElement>('.actions .resolve')?.disabled).toBe(false));
    document.querySelector<HTMLButtonElement>('.actions .resolve')!.click();
    await vi.waitFor(() => expect(api.resolveAgentNote).toHaveBeenCalledWith('task-1', 'note-1', 1));
  });

  it('disables creation at the six-note limit', async () => {
    vi.spyOn(api, 'listAgentNotes').mockResolvedValue({ data:Array.from({ length:6 }, (_, index) => note(`note-${index}`)), next_cursor:'' });
    mounted.push(mount(AgentNotes, { target:document.body, props:{ task, currentActorId:'agent-1' } }));
    await vi.waitFor(() => expect(document.body.textContent).toContain('6/6'));
    expect(document.querySelector<HTMLButtonElement>('.heading .text-button')?.disabled).toBe(true);
  });
});
