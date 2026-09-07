// @vitest-environment jsdom
import { mount, tick, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../api';
import type { Collection, Task, Watch } from '../types';
import TaskWatchToggle from './TaskWatchToggle.svelte';
import TaskWatchToggleTestHarness from './TaskWatchToggleTestHarness.svelte';

const mountedComponents: Array<ReturnType<typeof mount>> = [];

afterEach(async () => {
  while (mountedComponents.length) await unmount(mountedComponents.pop()!);
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

function task(id = 'task-1', key = 'TC-1'): Task {
  return {
    id,
    number: Number(key.split('-').at(-1)),
    key,
    project_id: 'project-1',
    column_id: 'column-1',
    title: key,
    priority: 'normal',
    position: 1,
    version: 1
  };
}

function watch(id: string, taskId: string): Watch {
  return { id, actor_id: 'actor-1', project_id: 'project-1', task_id: taskId, created_at: '2026-01-01T00:00:00Z' };
}

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => { resolve = complete; });
  return { promise, resolve };
}

async function settle(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
  await tick();
}

describe('TaskWatchToggle', () => {
  it('creates and deletes only the current task watch', async () => {
    const current = task();
    const created = watch('watch-task-1', current.id);
    const listWatches = vi.spyOn(api, 'listWatches').mockResolvedValue({ data: [] });
    const createWatch = vi.spyOn(api, 'createWatch').mockResolvedValue(created);
    const deleteWatch = vi.spyOn(api, 'deleteWatch').mockResolvedValue(undefined);
    mountedComponents.push(mount(TaskWatchToggle, { target: document.body, props: { task: current, sessionKey: 'actor-1:1' } }));

    await vi.waitFor(() => expect(listWatches).toHaveBeenCalledWith({ project: 'project-1', task: 'task-1' }));
    const button = document.querySelector<HTMLButtonElement>('[data-task-watch-toggle]')!;
    await vi.waitFor(() => expect(button.disabled).toBe(false));
    button.click();
    await vi.waitFor(() => expect(createWatch).toHaveBeenCalledWith({ task_id: 'task-1' }));
    await vi.waitFor(() => expect(button.getAttribute('aria-pressed')).toBe('true'));
    button.click();
    await vi.waitFor(() => expect(deleteWatch).toHaveBeenCalledWith('watch-task-1'));
  });

  it('ignores a delayed create after the drawer switches tasks', async () => {
    const first = task();
    const second = task('task-2', 'TC-2');
    const createdFirst = watch('watch-task-1', first.id);
    const existingSecond = watch('watch-task-2', second.id);
    const delayedCreate = deferred<Watch>();
    const listWatches = vi.spyOn(api, 'listWatches')
      .mockResolvedValueOnce({ data: [] })
      .mockResolvedValue({ data: [existingSecond] });
    const createWatch = vi.spyOn(api, 'createWatch').mockReturnValue(delayedCreate.promise);
    const deleteWatch = vi.spyOn(api, 'deleteWatch').mockResolvedValue(undefined);
    const mounted = mount(TaskWatchToggleTestHarness, { target: document.body, props: { task: first } });
    mountedComponents.push(mounted);

    await vi.waitFor(() => expect(listWatches).toHaveBeenCalledWith({ project: 'project-1', task: 'task-1' }));
    const firstButton = document.querySelector<HTMLButtonElement>('[data-task-watch-toggle]')!;
    await vi.waitFor(() => expect(firstButton.disabled).toBe(false));
    firstButton.click();
    await vi.waitFor(() => expect(createWatch).toHaveBeenCalledWith({ task_id: 'task-1' }));

    mounted.updateTask(second);
    await vi.waitFor(() => expect(listWatches).toHaveBeenCalledWith({ project: 'project-1', task: 'task-2' }));
    await vi.waitFor(() => expect(document.querySelector<HTMLButtonElement>('[data-task-watch-toggle]')?.getAttribute('aria-pressed')).toBe('true'));
    delayedCreate.resolve(createdFirst);
    await settle();

    expect(deleteWatch).not.toHaveBeenCalled();
    document.querySelector<HTMLButtonElement>('[data-task-watch-toggle]')!.click();
    await vi.waitFor(() => expect(deleteWatch).toHaveBeenCalledWith(existingSecond.id));
  });
});
