// Records local task mutations and reconciles server task snapshots into every view.
import { mergeAuthoritativeTask, taskMutationsAfter } from '../lib/liveness';
import type { TaskMutationKind, TaskMutationRecord, TaskMutationScope } from '../lib/liveness';
import { actorId, agentWorkState as workState, isAgentWorkStale as isWorkStale } from '../lib/state';
import type { Task } from '../lib/types';
import { syncBoardTaskPages } from './board/pages';
import { announce, workTransitionKey } from './feedback';
import { board } from './state/board.svelte';
import { drawer } from './state/drawer.svelte';
import { issues } from './state/issues.svelte';
import { live } from './state/live.svelte';
import { myWork } from './state/myWork.svelte';
import { palette } from './state/palette.svelte';
import { search } from './state/search.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';
import { claimIsActive, isActionNeeded } from './taskQueries';

export const taskMutations: Record<TaskMutationScope, Map<string, TaskMutationRecord>> = {
  board: new Map(),
  issues: new Map(),
  'my-work-live': new Map(),
  'my-work-assigned': new Map()
};

export function myWorkMutationScope(workView = myWork.myWorkView): TaskMutationScope {
  return workView === 'live' ? 'my-work-live' : 'my-work-assigned';
}

export function recordTaskMutation(
  taskId: string,
  kind: TaskMutationKind,
  scopes: TaskMutationScope[]
) {
  const revision = ++board.taskMutationRevision;
  scopes.forEach((scope) => {
    taskMutations[scope].set(taskId, { revision, kind });
  });
}

export function recordScopedTaskMutation(
  taskId: string,
  mutations: Partial<Record<TaskMutationScope, TaskMutationKind>>
) {
  const revision = ++board.taskMutationRevision;
  Object.entries(mutations).forEach(([scope, kind]) => {
    if (kind) taskMutations[scope as TaskMutationScope].set(taskId, { revision, kind });
  });
}

export function mutationsForRequest(scope: TaskMutationScope, requestRevision: number): Map<string, TaskMutationKind> {
  return taskMutationsAfter(taskMutations[scope], requestRevision);
}

export function replaceTask(updated: Task, localMutation = false, announceChanges = true) {
  const issueHasTask = issues.issueTasks.some((task) => task.id === updated.id);
  const existing = [
    drawer.drawerTask?.id === updated.id ? drawer.drawerTask : undefined,
    board.tasks.find((task) => task.id === updated.id),
    issues.issueTasks.find((task) => task.id === updated.id),
    myWork.myWorkTasks.find((task) => task.id === updated.id),
    search.searchTasks.find((task) => task.id === updated.id),
    palette.commandSearchTasks.find((task) => task.id === updated.id)
  ].filter((task): task is Task => Boolean(task));
  const previous = existing.reduce<Task | undefined>(
    (current, task) => !current || task.version > current.version ? task : current,
    undefined
  );
  const nextTask = mergeAuthoritativeTask(previous, updated);
  const belongsToActiveBoard = workspace.activeProject?.id === nextTask.project_id;
  if (belongsToActiveBoard) {
    board.tasks = board.tasks.some((task) => task.id === updated.id)
      ? board.tasks.map((task) => (task.id === updated.id ? mergeAuthoritativeTask(task, nextTask) : task))
      : [nextTask, ...board.tasks];
  }
  if (issues.issueTasks.some((task) => task.id === updated.id)) {
    issues.issueTasks = issues.issueTasks.map((task) => (task.id === updated.id ? mergeAuthoritativeTask(task, nextTask) : task));
  } else if (nextTask.kind === 'bug') {
    // A delete removes the task from the Issues cache. Restore must put a
    // bug back into the currently rendered Issues view without waiting for
    // the next 15-second event poll or a manual refresh.
    issues.issueTasks = [nextTask, ...issues.issueTasks];
  }
  if (drawer.drawerTask?.id === updated.id) {
    drawer.drawerTask = mergeAuthoritativeTask(drawer.drawerTask, nextTask);
  }
  if (search.searchTasks.some((task) => task.id === updated.id)) {
    search.searchTasks = search.searchTasks.map((task) => (task.id === updated.id ? mergeAuthoritativeTask(task, nextTask) : task));
  }
  if (palette.commandSearchTasks.some((task) => task.id === updated.id)) {
    palette.commandSearchTasks = palette.commandSearchTasks.map((task) => (task.id === updated.id ? mergeAuthoritativeTask(task, nextTask) : task));
  }
  const myWorkIndex = myWork.myWorkTasks.findIndex((task) => task.id === updated.id);
  const belongsToUser = actorId(nextTask.assignee) === session.user?.id
    || (actorId(nextTask.claimed_by) === session.user?.id && claimIsActive(nextTask, live.pulseClock));
  const keepInMyWork = myWork.myWorkView === 'live'
    ? !nextTask.completed_at && Boolean(nextTask.agent_work)
    : belongsToUser;
  if (localMutation) {
    const mutations: Partial<Record<TaskMutationScope, TaskMutationKind>> = {};
    if (belongsToActiveBoard) mutations.board = 'upsert';
    if (issueHasTask || nextTask.kind === 'bug') mutations.issues = 'upsert';
    if (myWorkIndex >= 0 || keepInMyWork) {
      mutations[myWorkMutationScope()] = myWorkIndex >= 0 && !keepInMyWork ? 'remove' : 'upsert';
    }
    recordScopedTaskMutation(updated.id, mutations);
  }
  if (myWorkIndex >= 0) {
    myWork.myWorkTasks = keepInMyWork
      ? myWork.myWorkTasks.map((task) => (task.id === updated.id ? mergeAuthoritativeTask(task, nextTask) : task))
      : myWork.myWorkTasks.filter((task) => task.id !== updated.id);
  } else if (keepInMyWork) {
    myWork.myWorkTasks = [...myWork.myWorkTasks, nextTask];
  }
  syncBoardTaskPages(nextTask, announceChanges);
  const previousTransition = previous ? workTransitionKey(previous) : '';
  const nextTransition = workTransitionKey(nextTask);
  if (announceChanges && previousTransition && nextTransition && previousTransition !== nextTransition) {
    const status = isWorkStale(nextTask) && workState(nextTask) !== 'waiting' ? 'stale' : workState(nextTask) || 'updated';
    announce(`${nextTask.key} is now ${status}${isActionNeeded(nextTask) ? ' · action needed' : ''}.`);
  }
}
