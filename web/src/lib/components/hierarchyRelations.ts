/** Pure helpers for the task hierarchy panel and its parent search. */
import type { Task as HierarchyTask, TaskHierarchy as HierarchyData, TaskHierarchyReference as HierarchyReference } from '../types';

/** Keep parent candidates project-local and exclude every known descendant. */
export function hierarchyCandidates(
  tasks: HierarchyTask[],
  currentTaskId: string,
  hierarchy: HierarchyData | null,
  query: string,
  limit = 20
): HierarchyTask[] {
  const normalized = query.trim().toLocaleLowerCase();
  if (!normalized) return [];
  const excluded = new Set<string>([
    currentTaskId,
    ...(hierarchy?.ancestors || []).map((item) => item.id),
    ...(hierarchy?.children || []).map((item) => item.id),
    ...(hierarchy?.descendants || []).map((item) => item.id)
  ]);
  return tasks
    .filter((candidate) => !excluded.has(candidate.id))
    .filter((candidate) => candidate.key.toLocaleLowerCase().includes(normalized)
      || candidate.title.toLocaleLowerCase().includes(normalized))
    .sort((left, right) => left.number - right.number || left.key.localeCompare(right.key))
    .slice(0, Math.max(1, limit));
}

export function nextHierarchyOptionIndex(current: number, key: string, count: number): number {
  if (count <= 0) return -1;
  if (key === 'Home') return 0;
  if (key === 'End') return count - 1;
  if (key === 'ArrowDown') return current < 0 ? 0 : (current + 1) % count;
  if (key === 'ArrowUp') return current < 0 ? count - 1 : (current - 1 + count) % count;
  return current;
}

export function hierarchyMutationMessage(error: { code?: string; message?: string } | null, fallback: string): string {
  switch (error?.code) {
    case 'stale_task': return 'This task changed elsewhere. The hierarchy was refreshed; review it and try again.';
    case 'hierarchy_cycle': return 'That parent would create a hierarchy cycle.';
    case 'hierarchy_self_reference': return 'A task cannot be its own parent.';
    case 'hierarchy_cross_project': return 'Parent and child tasks must belong to this project.';
    case 'hierarchy_already_exists': return 'That parent is already linked.';
    case 'hierarchy_limit_exceeded': return 'That parent has reached the direct child limit.';
    case 'hierarchy_depth_exceeded': return 'That relationship would exceed the hierarchy depth limit.';
    case 'hierarchy_in_use': return 'A parent with live children cannot be deleted.';
    case 'hierarchy_not_found': return 'That hierarchy relationship is no longer available.';
    case 'task_already_claimed': return 'Another actor currently owns this task claim.';
    default: return error?.message?.trim() || fallback;
  }
}

export function hierarchyReferenceTask(reference: HierarchyReference): HierarchyTask {
  return {
    id: reference.id,
    number: reference.number,
    key: reference.key,
    project_id: reference.project_id,
    title: reference.title,
    kind: reference.kind,
    column_id: reference.column_id,
    priority: 'normal',
    position: 0,
    version: reference.version,
    completed_at: reference.completed_at || undefined,
    agent_work: reference.agent_work
  };
}
