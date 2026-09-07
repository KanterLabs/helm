<script lang="ts">
  import NotificationsInbox from './NotificationsInbox.svelte';
  import type { Project, Task } from '../types';

  interface HarnessProps {
    project?: Project;
    task?: Task | null;
    sessionKey?: string;
  }

  let { project, task = null, sessionKey = 'actor-1:1' }: HarnessProps = $props();
  let currentProject = $state<Project | undefined>();
  let currentTask = $state<Task | null>(null);
  $effect(() => {
    currentProject = project;
    currentTask = task;
  });

  /** Test-only host control for replacing the active watch context. */
  export function updateContext(nextProject: Project | undefined, nextTask: Task | null): void {
    currentProject = nextProject;
    currentTask = nextTask;
  }
</script>

<NotificationsInbox sessionKey={sessionKey} activeProject={currentProject} activeTask={currentTask} />
