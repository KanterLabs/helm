<script lang="ts">
  import TaskShareActions from './TaskShareActions.svelte';
  import type { ClipboardWriter } from '../taskShare';

  type TaskShareHarnessTask = {
    taskKey: string;
    taskUrl: string;
  };
  type TaskShareHarnessProps = TaskShareHarnessTask & {
    clipboard?: ClipboardWriter | null;
  };

  let props: TaskShareHarnessProps = $props();
  let currentTaskKey = $state('');
  let currentTaskUrl = $state('');
  let currentClipboard = $state<ClipboardWriter | null | undefined>(undefined);

  $effect(() => {
    currentTaskKey = props.taskKey || '';
    currentTaskUrl = props.taskUrl || '';
    currentClipboard = props.clipboard;
  });

  /** Test-only host control for exercising a live drawer-task replacement. */
  export function updateTask(next: TaskShareHarnessTask): void {
    currentTaskKey = next.taskKey;
    currentTaskUrl = next.taskUrl;
  }
</script>

<TaskShareActions
  taskKey={currentTaskKey}
  taskUrl={currentTaskUrl}
  clipboard={currentClipboard}
/>
