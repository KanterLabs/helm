<script lang="ts">
  import TaskWatchToggle from './TaskWatchToggle.svelte';
  import type { Task } from '../types';

  interface HarnessProps {
    task: Task;
    sessionKey?: string;
  }

  let { task, sessionKey = 'actor-1:1' }: HarnessProps = $props();
  let currentTask = $state<Task | null>(null);
  $effect(() => {
    currentTask = task;
  });

  /** Test-only host control for switching the drawer's active task. */
  export function updateTask(nextTask: Task): void {
    currentTask = nextTask;
  }
</script>

{#if currentTask}<TaskWatchToggle task={currentTask} {sessionKey} />{/if}
