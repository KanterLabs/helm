// @vitest-environment jsdom
import { flushSync, mount, tick, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { mergeTimelineItems } from '../timeline';
import type { Comment, TaskTimelineItem } from '../types';
import TaskActivityTimeline from './TaskActivityTimeline.svelte';
import TimelineTestHarness from './TimelineTestHarness.svelte';

const mountedComponents: Array<ReturnType<typeof mount>> = [];

afterEach(async () => {
  while (mountedComponents.length) await unmount(mountedComponents.pop()!);
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

function cursor(eventCursor: number, kind: TaskTimelineItem['kind'], id: string): string {
  return btoa(JSON.stringify({ v: 1, at: '2026-01-01T00:00:00Z', ec: eventCursor, k: kind, id }))
    .replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function comment(body = 'Original comment', version = 1, taskId = 'task-1', id = 'comment-1'): Comment {
  return {
    id,
    task_id: taskId,
    actor_id: 'actor-1',
    body,
    version,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z'
  };
}

function commentItem(value = comment()): TaskTimelineItem {
  return {
    id: value.id,
    cursor: cursor(1, 'comment', value.id),
    kind: 'comment',
    task_id: value.task_id,
    actor: { id: value.actor_id, kind: 'human', name: 'Author' },
    created_at: value.created_at,
    progress: null,
    comment: value,
    change: null
  };
}

function progressItem(taskId = 'task-1'): TaskTimelineItem {
  return {
    id: `progress-${taskId}`,
    cursor: cursor(3, 'agent_progress', `progress-${taskId}`),
    kind: 'agent_progress',
    task_id: taskId,
    actor: { id: 'agent-1', kind: 'agent', name: 'Agent' },
    created_at: '2026-01-03T00:00:00Z',
    progress: {
      operation_id: 'operation-1',
      actor_id: 'agent-1',
      state: 'working',
      phase: 'Testing',
      summary: 'Running tests',
      next_action: 'Finish the task',
      checkpoint_refs: [],
      checkpoint_completed: 1,
      checkpoint_total: 2,
      started_at: '2026-01-03T00:00:00Z'
    },
    comment: null,
    change: null
  };
}

function deletedEvent(): TaskTimelineItem {
  return {
    id: 'delete-event',
    cursor: cursor(2, 'task_change', 'delete-event'),
    kind: 'task_change',
    task_id: 'task-1',
    actor: { id: 'actor-1', kind: 'human', name: 'Author' },
    created_at: '2026-01-02T00:00:00Z',
    progress: null,
    comment: null,
    change: { event_id: 'delete-event', event_type: 'comment.deleted', payload: { comment_id: 'comment-1' } }
  };
}

describe('TaskActivityTimeline component', () => {
  it('sends edited Markdown through the comment callback', async () => {
    const onEditComment = vi.fn().mockResolvedValue(undefined);
    const mounted = mount(TaskActivityTimeline, {
      target: document.body,
      props: { items: [commentItem()], currentActorId: 'actor-1', onEditComment }
    });
    mountedComponents.push(mounted);

    document.querySelector<HTMLButtonElement>('.task-timeline-comment-actions .text-button')?.click();
    await tick();
    const textarea = document.querySelector<HTMLTextAreaElement>('.task-timeline-comment-edit textarea');
    expect(textarea).not.toBeNull();
    textarea!.value = 'Edited with **Markdown**';
    textarea!.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector<HTMLButtonElement>('.task-timeline-comment-edit .button.primary')?.click();

    await vi.waitFor(() => expect(onEditComment).toHaveBeenCalledWith(expect.objectContaining({ id: 'comment-1' }), 'Edited with **Markdown**'));
  });

  it('recovers an active draft when a filter hides its comment row', async () => {
    let mounted: ReturnType<typeof mount>;
    const onFilterChange = vi.fn((next) => mounted.updateFilter(next));
    mounted = mount(TimelineTestHarness, {
      target: document.body,
      props: { items: [commentItem(), progressItem()], taskId: 'task-1', currentActorId: 'actor-1', onFilterChange }
    });
    mountedComponents.push(mounted);

    document.querySelector<HTMLButtonElement>('.task-timeline-comment-actions .text-button')?.click();
    await tick();
    const textarea = document.querySelector<HTMLTextAreaElement>('.task-timeline-comment-edit textarea');
    textarea!.value = 'Draft hidden by filter';
    textarea!.dispatchEvent(new Event('input', { bubbles: true }));

    mounted.updateFilter('agent_progress');
    flushSync();
    const recovery = document.querySelector<HTMLElement>('.task-timeline-draft-recovery');
    expect(recovery).not.toBeNull();
    expect(recovery?.textContent).toContain('hidden by the “Agent” filter');
    expect(recovery?.querySelector<HTMLTextAreaElement>('textarea')?.value).toBe('Draft hidden by filter');
    expect(recovery?.textContent).toContain('Show comment');

    recovery?.querySelector<HTMLButtonElement>('.text-button')?.click();
    await tick();
    expect(onFilterChange).toHaveBeenCalledWith('all');
    expect(document.querySelector<HTMLTextAreaElement>('.task-timeline-comment-edit textarea')?.value).toBe('Draft hidden by filter');
  });

  it('freezes the submitted body during a delayed save and clears only after success', async () => {
    let resolveSave!: () => void;
    const onEditComment = vi.fn(() => new Promise<void>((resolve) => { resolveSave = resolve; }));
    const mounted = mount(TaskActivityTimeline, {
      target: document.body,
      props: { items: [commentItem()], taskId: 'task-1', currentActorId: 'actor-1', onEditComment }
    });
    mountedComponents.push(mounted);

    document.querySelector<HTMLButtonElement>('.task-timeline-comment-actions .text-button')?.click();
    await tick();
    const textarea = document.querySelector<HTMLTextAreaElement>('.task-timeline-comment-edit textarea');
    textarea!.value = 'Submitted once';
    textarea!.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector<HTMLButtonElement>('.task-timeline-comment-edit .button.primary')?.click();

    await vi.waitFor(() => expect(onEditComment).toHaveBeenCalledWith(expect.objectContaining({ id: 'comment-1' }), 'Submitted once'));
    flushSync();
    expect(textarea!.disabled).toBe(true);
    expect(textarea!.value).toBe('Submitted once');
    resolveSave();
    await vi.waitFor(() => expect(document.querySelector('.task-timeline-comment-edit')).toBeNull());
  });

  it('preserves a failed save draft and allows retrying the same text', async () => {
    let attempt = 0;
    const onEditComment = vi.fn(async (_comment: Comment, _body: string) => {
      attempt += 1;
      if (attempt === 1) throw new Error('network unavailable');
    });
    const mounted = mount(TaskActivityTimeline, {
      target: document.body,
      props: { items: [commentItem()], taskId: 'task-1', currentActorId: 'actor-1', onEditComment }
    });
    mountedComponents.push(mounted);

    document.querySelector<HTMLButtonElement>('.task-timeline-comment-actions .text-button')?.click();
    await tick();
    const textarea = document.querySelector<HTMLTextAreaElement>('.task-timeline-comment-edit textarea');
    textarea!.value = 'Retry this draft';
    textarea!.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector<HTMLButtonElement>('.task-timeline-comment-edit .button.primary')?.click();

    await tick();
    flushSync();
    expect(document.body.textContent).toContain('network unavailable');
    expect(textarea!.value).toBe('Retry this draft');
    document.querySelector<HTMLButtonElement>('.task-timeline-comment-edit .button.primary')?.click();
    await vi.waitFor(() => expect(onEditComment).toHaveBeenCalledTimes(2));
    expect(onEditComment.mock.calls[1]?.[1]).toBe('Retry this draft');
    await vi.waitFor(() => expect(document.querySelector('.task-timeline-comment-edit')).toBeNull());
  });

  it('sends a confirmed deletion through the comment callback', async () => {
    const onConfirmDelete = vi.fn().mockResolvedValue(true);
    const onDeleteComment = vi.fn().mockResolvedValue(undefined);
    const mounted = mount(TaskActivityTimeline, {
      target: document.body,
      props: { items: [commentItem()], currentActorId: 'actor-1', onConfirmDelete, onDeleteComment }
    });
    mountedComponents.push(mounted);

    const deleteButton = [...document.querySelectorAll<HTMLButtonElement>('.task-timeline-comment-actions .text-button')]
      .find((button) => button.textContent?.trim() === 'Delete');
    deleteButton?.click();
    await vi.waitFor(() => expect(onDeleteComment).toHaveBeenCalledWith(expect.objectContaining({ id: 'comment-1' })));
    expect(onConfirmDelete).toHaveBeenCalledWith(expect.objectContaining({ id: 'comment-1' }));
  });

  it('does not delete a comment when confirmation is canceled', async () => {
    const onConfirmDelete = vi.fn().mockResolvedValue(false);
    const onDeleteComment = vi.fn().mockResolvedValue(undefined);
    const mounted = mount(TaskActivityTimeline, {
      target: document.body,
      props: { items: [commentItem()], currentActorId: 'actor-1', onConfirmDelete, onDeleteComment }
    });
    mountedComponents.push(mounted);

    const deleteButton = [...document.querySelectorAll<HTMLButtonElement>('.task-timeline-comment-actions .text-button')]
      .find((button) => button.textContent?.trim() === 'Delete');
    deleteButton?.click();
    await vi.waitFor(() => expect(onConfirmDelete).toHaveBeenCalledOnce());
    expect(onDeleteComment).not.toHaveBeenCalled();
  });

  it('keeps a local edit draft while replacing stale content and retains the delete event', async () => {
    const original = commentItem();
    const mounted = mount(TimelineTestHarness, {
      target: document.body,
      props: { items: [original], currentActorId: 'actor-1' }
    });
    mountedComponents.push(mounted);

    document.querySelector<HTMLButtonElement>('.task-timeline-comment-actions .text-button')?.click();
    await tick();
    const textarea = document.querySelector<HTMLTextAreaElement>('.task-timeline-comment-edit textarea');
    textarea!.value = 'Local draft not yet saved';
    textarea!.dispatchEvent(new Event('input', { bubbles: true }));

    const edited = mergeTimelineItems(
      [original],
      [],
      { updatedComments: new Map([['comment-1', comment('Remote canonical edit', 2)]]) }
    );
    mounted.updateItems(edited);
    flushSync();
    expect(textarea!.value).toBe('Local draft not yet saved');
    expect(edited[0]?.comment?.body).toBe('Remote canonical edit');

    document.querySelector<HTMLButtonElement>('.task-timeline-comment-edit .text-button')?.click();
    await tick();
    expect(document.body.textContent).toContain('Remote canonical edit');

    mounted.updateItems([deletedEvent()]);
    flushSync();
    expect(document.body.textContent).not.toContain('Original comment');
    expect(document.body.textContent).toContain('deleted a comment');
  });

  it('keeps the original comment version when a newer remote row arrives', async () => {
    const original = commentItem(comment('Original comment', 1));
    const onEditComment = vi.fn().mockResolvedValue(undefined);
    const mounted = mount(TimelineTestHarness, {
      target: document.body,
      props: { items: [original], taskId: 'task-1', currentActorId: 'actor-1', onEditComment }
    });
    mountedComponents.push(mounted);

    document.querySelector<HTMLButtonElement>('.task-timeline-comment-actions .text-button')?.click();
    await tick();
    const textarea = document.querySelector<HTMLTextAreaElement>('.task-timeline-comment-edit textarea');
    textarea!.value = 'Local draft wins the editor';
    textarea!.dispatchEvent(new Event('input', { bubbles: true }));

    const remote = mergeTimelineItems(
      [original],
      [],
      { updatedComments: new Map([['comment-1', comment('Remote canonical edit', 2)]]) }
    );
    mounted.updateItems(remote);
    flushSync();
    expect(textarea!.value).toBe('Local draft wins the editor');
    expect(document.body.textContent).toContain('This comment changed remotely');

    document.querySelector<HTMLButtonElement>('.task-timeline-comment-edit .button.primary')?.click();
    await vi.waitFor(() => expect(onEditComment).toHaveBeenCalledOnce());
    expect(onEditComment.mock.calls[0]?.[0]).toMatchObject({ id: 'comment-1', body: 'Original comment', version: 1 });
    expect(onEditComment.mock.calls[0]?.[1]).toBe('Local draft wins the editor');
  });

  it('isolates drafts at task boundaries and restores only the matching task draft', async () => {
    const taskOne = commentItem(comment('Task one canonical', 1, 'task-1', 'comment-1'));
    const taskTwo = commentItem(comment('Task two canonical', 1, 'task-2', 'comment-2'));
    const mounted = mount(TimelineTestHarness, {
      target: document.body,
      props: { items: [taskOne], taskId: 'task-1', currentActorId: 'actor-1' }
    });
    mountedComponents.push(mounted);

    document.querySelector<HTMLButtonElement>('.task-timeline-comment-actions .text-button')?.click();
    await tick();
    const textarea = document.querySelector<HTMLTextAreaElement>('.task-timeline-comment-edit textarea');
    textarea!.value = 'Task one private draft';
    textarea!.dispatchEvent(new Event('input', { bubbles: true }));

    mounted.updateTaskId('task-2');
    mounted.updateItems([taskTwo]);
    flushSync();
    expect(document.querySelector('.task-timeline-comment-edit')).toBeNull();
    expect(document.body.textContent).toContain('Task two canonical');
    expect(document.body.textContent).not.toContain('Task one private draft');

    mounted.updateTaskId('task-1');
    mounted.updateItems([taskOne]);
    flushSync();
    expect(document.querySelector<HTMLTextAreaElement>('.task-timeline-comment-edit textarea')?.value).toBe('Task one private draft');
  });
});
