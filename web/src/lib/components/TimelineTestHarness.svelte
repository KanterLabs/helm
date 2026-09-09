<script lang="ts">
  import TaskActivityTimeline from './TaskActivityTimeline.svelte';
  import type { Comment, TaskTimelineFilter, TaskTimelineItem } from '../types';

  interface HarnessProps {
    items?: TaskTimelineItem[];
    taskId?: string;
    filter?: TaskTimelineFilter;
    currentActorId?: string;
    canManageComments?: boolean;
    onFilterChange?: (next: TaskTimelineFilter) => void;
    onEditComment?: (comment: Comment, body: string) => void | Promise<void>;
    onDeleteComment?: (comment: Comment) => void | Promise<void>;
  }

  let {
    items = [],
    taskId = '',
    filter = 'all',
    currentActorId = '',
    canManageComments = false,
    onFilterChange = () => undefined,
    onEditComment = () => undefined,
    onDeleteComment = () => undefined
  }: HarnessProps = $props();
  let currentItems = $state(undefined as TaskTimelineItem[] | undefined);
  let currentTaskId = $state('');
  let currentFilter = $state('all' as TaskTimelineFilter);
  $effect(() => {
    currentItems = items;
    currentTaskId = taskId;
    currentFilter = filter;
  });

  /** Test-only host control for exercising a live prop replacement in Svelte 5. */
  export function updateItems(next: TaskTimelineItem[]): void {
    currentItems = next;
  }

  /** Test-only host controls for exercising task and filter boundaries. */
  export function updateTaskId(next: string): void {
    currentTaskId = next;
  }

  export function updateFilter(next: TaskTimelineFilter): void {
    currentFilter = next;
  }
</script>

<TaskActivityTimeline
  items={currentItems ?? items}
  filter={currentFilter}
  taskId={currentTaskId}
  {currentActorId}
  {canManageComments}
  {onFilterChange}
  {onEditComment}
  {onDeleteComment}
/>
