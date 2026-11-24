import type { Project } from './types';

export function projectCapacity(project: Project | undefined) {
  const count = project?.active_task_count;
  const limit = project?.active_task_limit;
  if (typeof count !== 'number' || typeof limit !== 'number') return;
  return { count, limit };
}

export function projectCapacityAction(count?: number, limit?: number) {
  const slots = count !== undefined && limit !== undefined ? count - limit + 1 : 1;
  return slots > 1
    ? `Finish or move ${slots} In Progress tasks to free a slot.`
    : 'Finish or move an In Progress task to free a slot.';
}

export function capacityRefusal(error: { code: string; details?: Record<string, unknown> }) {
  if (error.code === 'project_capacity_reached') {
    const count = error.details?.active_count;
    const limit = error.details?.limit;
    return `In Progress is at capacity for this project. ${projectCapacityAction(
      typeof count === 'number' ? count : undefined, typeof limit === 'number' ? limit : undefined
    )}`;
  }
  if (error.code !== 'agent_already_active') return '';
  const keys = error.details?.task_keys;
  const existing = Array.isArray(keys) ? keys.filter((key): key is string => typeof key === 'string').join(', ') : '';
  return existing
    ? `This agent already owns active work on ${existing}. Finish or hand off that work before starting another task.`
    : 'This agent already owns active work. Finish or hand off that work before starting another task.';
}
