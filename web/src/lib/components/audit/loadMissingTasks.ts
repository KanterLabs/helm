import type { Task } from '../../types';

/**
 * Findings can reference tasks beyond the loaded board page. Fetch only the
 * missing identities, a few at a time, so titles and links stay correct.
 */
export async function loadMissingTasks(ids: string[], getTask: (id: string) => Promise<Task>, limit = 100): Promise<Task[]> {
  const queue = [...new Set(ids)].slice(0, limit);
  const found: Task[] = [];
  const worker = async () => {
    for (let id = queue.shift(); id; id = queue.shift()) {
      try {
        found.push(await getTask(id));
      } catch {
        // Deleted or forbidden tasks keep their captured ID in the finding.
      }
    }
  };
  await Promise.all([worker(), worker(), worker(), worker()]);
  return found;
}
