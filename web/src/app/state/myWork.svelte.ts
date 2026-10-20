// Reactive state: My work: live and assigned task lists.
import { agentWorkBucket as workBucket } from '../../lib/state';
import type { Column, Task } from '../../lib/types';
import { isActionNeeded, matchesWorkFilter, projectForTask, sortWorkRows, taskMatchesReleaseFilter } from '../taskQueries';
import type { MyWorkView, WorkFilter } from '../types';
import { live } from './live.svelte';

class MyWorkState {
  myWorkRequest = $state.raw(0);
  myWorkTasks = $state.raw<Task[]>([]);
  myWorkLoading = $state.raw(false);
  myWorkError = $state.raw('');
  myWorkFilter = $state.raw<WorkFilter>('all');
  myWorkView = $state.raw<MyWorkView>('live');
  myWorkReleaseFilter = $state.raw('all');
  myWorkColumnsByProject = $state.raw<Record<string, Column[]>>({});
  myWorkColumnsLoading = $state.raw(false);

  // Derived values
  visibleMyWorkTasks: Task[] = $derived(myWork.myWorkTasks.filter((task) => taskMatchesReleaseFilter(task, myWork.myWorkReleaseFilter)));
  myWorkRows = $derived(myWork.visibleMyWorkTasks.map((task) => ({
    task,
    project: projectForTask(task),
    column: myWork.myWorkColumnsByProject[task.project_id]?.find((item) => item.id === task.column_id)
  })).filter((row) => matchesWorkFilter(row.task, myWork.myWorkFilter, live.pulseClock)));
  myWorkActionRows = $derived(sortWorkRows(myWork.myWorkRows.filter((row) => isActionNeeded(row.task, live.pulseClock))));
  myWorkStaleRows = $derived(sortWorkRows(myWork.myWorkRows.filter((row) => workBucket(row.task, live.pulseClock) === 'stale')));
  myWorkWaitingRows = $derived(sortWorkRows(myWork.myWorkRows.filter((row) => workBucket(row.task, live.pulseClock) === 'waiting')));
  myWorkHandoffRows = $derived(sortWorkRows(myWork.myWorkRows.filter((row) => workBucket(row.task, live.pulseClock) === 'handoff')));
  myWorkVerifyingRows = $derived(sortWorkRows(myWork.myWorkRows.filter((row) => workBucket(row.task, live.pulseClock) === 'verifying')));
  myWorkWorkingRows = $derived(sortWorkRows(myWork.myWorkRows.filter((row) => workBucket(row.task, live.pulseClock) === 'working')));
}

export const myWork = new MyWorkState();
