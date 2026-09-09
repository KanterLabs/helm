import { mount, tick, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import TaskShareActions, {
  taskShareCopyLabel,
  taskShareFeedbackMessage
} from './TaskShareActions.svelte';
import TaskShareActionsTestHarness from './TaskShareActionsTestHarness.svelte';

const mounted: Array<ReturnType<typeof mount>> = [];

afterEach(async () => {
  while (mounted.length) await unmount(mounted.pop()!);
  document.body.replaceChildren();
});

async function flush(): Promise<void> {
  await Promise.resolve();
  await tick();
}

describe('TaskShareActions', () => {
  it('names the two stable values and reports successful copies accessibly', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    mounted.push(mount(TaskShareActions, {
      target: document.body,
      props: {
        taskKey: 'OPS-7',
        taskUrl: 'https://helm.example/p/ops/tasks/OPS-7',
        clipboard: { writeText }
      }
    }));
    await tick();

    const share = document.querySelector<HTMLElement>('[data-task-share]');
    expect(share?.getAttribute('aria-label')).toBe('Share task');
    expect(document.querySelector('[data-task-share-copy="link"]')?.textContent).toContain('Copy link');
    expect(document.querySelector('[data-task-share-copy="key"]')?.textContent).toContain('Copy key');
    expect(document.querySelector('[data-task-share-fallback]')).toBeNull();

    document.querySelector<HTMLButtonElement>('[data-task-share-copy="link"]')?.click();
    await flush();
    expect(writeText).toHaveBeenCalledWith('https://helm.example/p/ops/tasks/OPS-7');
    expect(document.querySelector('[role="status"]')?.textContent).toContain('Task link copied');
    expect(document.querySelector('[role="alert"]')).toBeNull();
  });

  it('does not claim success when clipboard access is absent and exposes manual fallback', async () => {
    mounted.push(mount(TaskShareActions, {
      target: document.body,
      props: {
        taskKey: 'OPS-7',
        taskUrl: 'https://helm.example/p/ops/tasks/OPS-7',
        clipboard: null
      }
    }));
    await tick();

    document.querySelector<HTMLButtonElement>('[data-task-share-copy="key"]')?.click();
    await flush();
    expect(document.querySelector('[role="status"]')).toBeNull();
    const alert = document.querySelector('[role="alert"]');
    expect(alert?.textContent).toContain('Couldn’t copy task key');
    expect(alert?.textContent).toContain('copy it manually');
    const fallback = document.querySelector<HTMLInputElement>('[data-task-share-fallback="key"]');
    expect(fallback).toBeTruthy();
    expect(fallback?.readOnly).toBe(true);
    expect(fallback?.value).toBe('OPS-7');
  });

  it('ignores a delayed result after switching to another drawer task', async () => {
    const resolvers: Array<() => void> = [];
    const writeText = vi.fn(() => new Promise<void>((resolve) => resolvers.push(resolve)));
    const component = mount(TaskShareActionsTestHarness, {
      target: document.body,
      props: {
        taskKey: 'OPS-7',
        taskUrl: 'https://helm.example/p/ops/tasks/OPS-7',
        clipboard: { writeText }
      }
    });
    mounted.push(component);
    await tick();

    document.querySelector<HTMLButtonElement>('[data-task-share-copy="link"]')?.click();
    await tick();
    expect(document.querySelector('[data-task-share-copy="link"]')?.textContent).toContain('Copying…');

    (component as unknown as { updateTask: (task: { taskKey: string; taskUrl: string }) => void }).updateTask({
      taskKey: 'OPS-8',
      taskUrl: 'https://helm.example/p/ops/tasks/OPS-8'
    });
    await tick();
    expect(document.querySelector('[data-task-share-copy="link"]')?.textContent).toContain('Copy link');
    expect(document.querySelector('[role="status"]')).toBeNull();

    resolvers.shift()?.();
    await flush();
    expect(document.querySelector('[role="status"]')).toBeNull();
    expect(document.querySelector('[role="alert"]')).toBeNull();
    expect(document.querySelector('[data-task-share-copy="link"]')?.textContent).toContain('Copy link');

    document.querySelector<HTMLButtonElement>('[data-task-share-copy="key"]')?.click();
    await tick();
    expect(document.querySelector('[data-task-share-copy="key"]')?.textContent).toContain('Copying…');
    resolvers.shift()?.();
    await flush();
    expect(document.querySelector('[role="status"]')?.textContent).toContain('Task key copied');
  });

  it('keeps copy labels and feedback text consistent', () => {
    expect(taskShareCopyLabel('link')).toBe('task link');
    expect(taskShareCopyLabel('key')).toBe('task key');
    expect(taskShareFeedbackMessage('link', true)).toBe('Task link copied to clipboard.');
    expect(taskShareFeedbackMessage('key', false)).toContain('Couldn’t copy task key');
  });
});
