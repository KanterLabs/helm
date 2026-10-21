/** Pure helpers for the task dependency panel and its prerequisite search. */
import type {
  Task as DependencyTask,
  TaskDependencies as DependencyRelations,
  TaskReference as DependencyReference
} from '../types';

/** Keep combobox filtering deterministic, project-local, and cycle-aware. */
export function dependencyCandidates(
  tasks: DependencyTask[],
  currentTaskId: string,
  relations: DependencyRelations | null,
  query: string,
  limit = 20
): DependencyTask[] {
  const normalized = query.trim().toLocaleLowerCase();
  if (!normalized) return [];
  const excluded = new Set<string>([
    currentTaskId,
    ...(relations?.prerequisites || []).map((item) => item.id),
    ...(relations?.dependents || []).map((item) => item.id)
  ]);
  return tasks
    .filter((candidate) => !excluded.has(candidate.id))
    .filter((candidate) =>
      candidate.key.toLocaleLowerCase().includes(normalized)
      || candidate.title.toLocaleLowerCase().includes(normalized)
    )
    .sort((left, right) => left.number - right.number || left.key.localeCompare(right.key))
    .slice(0, Math.max(1, limit));
}

/** Return the next active option for the combobox's keyboard contract. */
export function nextDependencyOptionIndex(current: number, key: string, count: number): number {
  if (count <= 0) return -1;
  if (key === 'Home') return 0;
  if (key === 'End') return count - 1;
  if (key === 'ArrowDown') return current < 0 ? 0 : (current + 1) % count;
  if (key === 'ArrowUp') return current < 0 ? count - 1 : (current - 1 + count) % count;
  return current;
}

export function dependencyStateLabel(reference: DependencyReference): string {
  return reference.satisfied ? 'Done' : 'Open';
}

export function dependencyMutationMessage(error: { code?: string; message?: string } | null, fallback: string): string {
  switch (error?.code) {
    case 'stale_task': return 'This task changed elsewhere. Relationships were refreshed; review them and try again.';
    case 'dependency_cycle': return 'That prerequisite would create a dependency cycle.';
    case 'dependency_already_exists': return 'That prerequisite is already linked.';
    case 'dependency_self_reference': return 'A task cannot depend on itself.';
    case 'dependency_cross_project': return 'Prerequisites must belong to this project.';
    case 'dependency_limit_exceeded': return 'This task has reached its direct dependency limit.';
    case 'dependency_not_found': return 'That task is no longer available.';
    case 'unmet_dependencies': return 'An unfinished prerequisite cannot be added after work has started.';
    case 'task_already_claimed': return 'Another actor currently owns this task claim.';
    default: return error?.message?.trim() || fallback;
  }
}
