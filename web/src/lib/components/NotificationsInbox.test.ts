// @vitest-environment jsdom
import { mount, tick, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../api';
import type { Collection, Notification, NotificationPreferences, Project, Task, Watch } from '../types';
import NotificationsInbox from './NotificationsInbox.svelte';
import NotificationsInboxTestHarness from './NotificationsInboxTestHarness.svelte';

const mountedComponents: Array<ReturnType<typeof mount>> = [];

afterEach(async () => {
  while (mountedComponents.length) await unmount(mountedComponents.pop()!);
  document.body.replaceChildren();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (reason?: unknown) => void } {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((complete, fail) => {
    resolve = complete;
    reject = fail;
  });
  return { promise, resolve, reject };
}

function notification(id: string, readAt: string | null = null): Notification {
  return {
    id,
    recipient_id: 'actor-1',
    actor_id: 'actor-2',
    event_type: 'task.updated',
    project_id: 'project-1',
    task_id: 'task-1',
    title: `Notification ${id}`,
    body: 'A task changed.',
    payload: { url: '/do-not-trust-this' },
    dedupe_key: id,
    read_at: readAt,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z'
  };
}

function project(): Project {
  return {
    id: 'project-1',
    key: 'TC',
    slug: 'task-coordination',
    name: 'Task Coordination',
    description: '',
    color: '#6558d8',
    favorite: false
  };
}

function task(): Task {
  return {
    id: 'task-1',
    number: 1,
    key: 'TC-1',
    project_id: 'project-1',
    column_id: 'column-1',
    title: 'Task one',
    priority: 'normal',
    position: 1,
    version: 1
  };
}

function preferences(): NotificationPreferences {
  return {
    actor_id: 'actor-1',
    assignments: true,
    mentions: true,
    blockers: false,
    state_changes: true,
    updated_at: '2026-01-01T00:00:00Z'
  };
}

function watch(id: string, taskId: string | null = null): Watch {
  return { id, actor_id: 'actor-1', project_id: 'project-1', task_id: taskId, created_at: '2026-01-01T00:00:00Z' };
}

async function settle(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
  await tick();
}

describe('NotificationsInbox', () => {
  it('opens a notification through the callback and marks it read', async () => {
    const current = notification('notification-1');
    const list = vi.spyOn(api, 'listNotifications').mockResolvedValue({ data: [current], next_cursor: null });
    vi.spyOn(api, 'listWatches').mockResolvedValue({ data: [] });
    const markRead = vi.spyOn(api, 'markNotificationRead').mockResolvedValue({ ...current, read_at: '2026-01-01T01:00:00Z' });
    const onOpen = vi.fn();
    mountedComponents.push(mount(NotificationsInbox, { target: document.body, props: { sessionKey: 'actor-1:1', onOpenNotification: onOpen } }));

    await vi.waitFor(() => expect(list).toHaveBeenCalled());
    document.querySelector<HTMLButtonElement>('.notifications-trigger')!.click();
    await vi.waitFor(() => expect(document.querySelector('[data-notification-open="notification-1"]')).not.toBeNull());
    document.querySelector<HTMLButtonElement>('[data-notification-open="notification-1"]')!.click();

    await vi.waitFor(() => expect(markRead).toHaveBeenCalledWith('notification-1', true));
    await vi.waitFor(() => expect(onOpen).toHaveBeenCalledWith(current));
    await vi.waitFor(() => expect(document.querySelector('.notifications-trigger')?.getAttribute('aria-label')).toBe('Open notifications'));
    expect(document.querySelector('.notifications-heading p')?.textContent).toBe('All caught up');
  });

  it('keeps pagination honest when the loaded page has no unread rows', async () => {
    const first = notification('notification-1', '2026-01-01T01:00:00Z');
    const second = notification('notification-2', '2026-01-01T02:00:00Z');
    const list = vi.spyOn(api, 'listNotifications')
      .mockResolvedValueOnce({ data: [first], next_cursor: 'cursor-2' })
      .mockResolvedValueOnce({ data: [first], next_cursor: 'cursor-2' })
      .mockResolvedValueOnce({ data: [second], next_cursor: null });
    vi.spyOn(api, 'listWatches').mockResolvedValue({ data: [] });
    mountedComponents.push(mount(NotificationsInbox, { target: document.body, props: { sessionKey: 'actor-1:1' } }));

    await vi.waitFor(() => expect(list).toHaveBeenCalled());
    document.querySelector<HTMLButtonElement>('.notifications-trigger')!.click();
    await vi.waitFor(() => expect(document.body.textContent).toContain('No unread on this page'));
    document.querySelector<HTMLButtonElement>('.notifications-more')!.click();
    await vi.waitFor(() => expect(document.body.textContent).toContain('Notification notification-2'));
    expect(list).toHaveBeenLastCalledWith({ limit: 25, cursor: 'cursor-2' });
    expect(document.querySelector('.notifications-heading p')?.textContent).toBe('All caught up');
  });

  it('does not claim the inbox is caught up when loading fails', async () => {
    vi.spyOn(api, 'listNotifications').mockRejectedValue(new Error('Temporary failure'));
    vi.spyOn(api, 'listWatches').mockResolvedValue({ data: [] });
    mountedComponents.push(mount(NotificationsInbox, { target: document.body, props: { sessionKey: 'actor-1:1' } }));
    await settle();
    document.querySelector<HTMLButtonElement>('.notifications-trigger')!.click();
    await vi.waitFor(() => expect(document.querySelector('.notifications-heading p')?.textContent).toBe('Inbox needs attention'));
    expect(document.body.textContent).not.toContain('All caught up');
  });

  it('ignores a stale poll response when a read mutation starts while it is pending', async () => {
    vi.useFakeTimers();
    const current = notification('notification-1');
    const stalePoll = deferred<Collection<Notification>>();
    const updated = deferred<Notification>();
    const list = vi.spyOn(api, 'listNotifications')
      .mockResolvedValueOnce({ data: [current], next_cursor: null })
      .mockReturnValueOnce(stalePoll.promise);
    vi.spyOn(api, 'listWatches').mockResolvedValue({ data: [] });
    const markRead = vi.spyOn(api, 'markNotificationRead').mockReturnValue(updated.promise);
    mountedComponents.push(mount(NotificationsInbox, { target: document.body, props: { sessionKey: 'actor-1:1', pollIntervalMs: 10 } }));
    await settle();

    vi.advanceTimersByTime(10);
    await settle();
    expect(list).toHaveBeenCalledTimes(2);
    document.querySelector<HTMLButtonElement>('.notifications-trigger')!.click();
    await tick();
    document.querySelector<HTMLButtonElement>('[data-notification-read-toggle="notification-1"]')!.click();
    expect(markRead).toHaveBeenCalledWith('notification-1', true);

    stalePoll.resolve({ data: [{ ...current, title: 'Stale title' }], next_cursor: null });
    updated.resolve({ ...current, read_at: '2026-01-01T01:00:00Z' });
    await settle();
    expect(document.body.textContent).not.toContain('Stale title');
    expect(document.querySelector<HTMLElement>('.notification-item')?.classList.contains('unread')).toBe(false);
  });

  it('exposes project/task watch controls and only the active preference controls', async () => {
    const projectWatch = watch('watch-project');
    const listWatches = vi.spyOn(api, 'listWatches').mockResolvedValue({ data: [projectWatch] });
    vi.spyOn(api, 'listNotifications').mockResolvedValue({ data: [], next_cursor: null });
    const createdTaskWatch = watch('watch-task', 'task-1');
    const createWatch = vi.spyOn(api, 'createWatch').mockResolvedValue(createdTaskWatch);
    vi.spyOn(api, 'getNotificationPreferences').mockResolvedValue(preferences());
    const patchPreferences = vi.spyOn(api, 'patchNotificationPreferences').mockResolvedValue({ ...preferences(), blockers: true });
    mountedComponents.push(mount(NotificationsInbox, { target: document.body, props: { sessionKey: 'actor-1:1', activeProject: project(), activeTask: task() } }));

    await vi.waitFor(() => expect(listWatches).toHaveBeenCalled());
    document.querySelector<HTMLButtonElement>('.notifications-trigger')!.click();
    await vi.waitFor(() => expect(document.body.textContent).toContain('Unwatch project'));
    expect(document.body.textContent).toContain('Watch task');
    document.querySelector<HTMLButtonElement>('.watch-toggle:nth-child(2)')!.click();
    await vi.waitFor(() => expect(createWatch).toHaveBeenCalledWith({ task_id: 'task-1' }));

    document.querySelector<HTMLButtonElement>('.notifications-preferences-trigger')!.click();
    await vi.waitFor(() => expect(document.body.textContent).toContain('Assignments'));
    expect(document.body.textContent).not.toContain('Due dates');
    expect(document.body.textContent).not.toContain('Claim expiry');
    const blocker = [...document.querySelectorAll<HTMLInputElement>('.notifications-preferences-panel input')].find((input) => input.parentElement?.textContent?.includes('Blockers'));
    expect(blocker).not.toBeUndefined();
    blocker!.click();
    await vi.waitFor(() => expect(patchPreferences).toHaveBeenCalledWith({ blockers: true }));
    expect(listWatches).toHaveBeenCalledWith({ project: 'project-1', task: 'task-1' });
  });

  it('does not let a delayed watch response for the old task change the new task state', async () => {
    const stale = deferred<Collection<Watch>>();
    const listWatches = vi.spyOn(api, 'listWatches')
      .mockReturnValueOnce(stale.promise)
      .mockResolvedValueOnce({ data: [] });
    vi.spyOn(api, 'listNotifications').mockResolvedValue({ data: [], next_cursor: null });
    const mounted = mount(NotificationsInboxTestHarness, { target: document.body, props: { project: project(), task: task() } });
    mountedComponents.push(mounted);

    await vi.waitFor(() => expect(listWatches).toHaveBeenCalledWith({ project: 'project-1', task: 'task-1' }));
    const nextTask = { ...task(), id: 'task-2', key: 'TC-2' };
    mounted.updateContext(project(), nextTask);
    await vi.waitFor(() => expect(listWatches).toHaveBeenCalledWith({ project: 'project-1', task: 'task-2' }));
    stale.resolve({ data: [watch('stale-task-watch', 'task-1')] });
    await settle();

    document.querySelector<HTMLButtonElement>('.notifications-trigger')!.click();
    await vi.waitFor(() => expect(document.body.textContent).toContain('Watch task'));
    expect(document.body.textContent).not.toContain('Unwatch task');
  });

  it('clears cached rows when the session is invalidated', async () => {
    const list = vi.spyOn(api, 'listNotifications').mockResolvedValue({ data: [notification('notification-1')], next_cursor: null });
    vi.spyOn(api, 'listWatches').mockResolvedValue({ data: [] });
    mountedComponents.push(mount(NotificationsInbox, { target: document.body, props: { sessionKey: 'actor-1:1' } }));
    await vi.waitFor(() => expect(list).toHaveBeenCalled());
    document.querySelector<HTMLButtonElement>('.notifications-trigger')!.click();
    await vi.waitFor(() => expect(document.body.textContent).toContain('Notification notification-1'));
    window.dispatchEvent(new Event('helm:auth-invalidated'));
    await tick();
    expect(document.body.textContent).not.toContain('Notification notification-1');
    expect(document.body.textContent).toContain('Notifications are unavailable until your session is renewed.');
  });
});
