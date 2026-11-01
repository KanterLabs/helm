import { api } from './api';
import type { Column, Task } from './types';

/** Keep the UI's intake recognition aligned with the store's optional lane. */
export function isBugIntake(column: Column): boolean {
  return !column.archived_at && column.semantic_state === 'backlog' && column.name.trim().toLowerCase() === 'bugs';
}

/** Triage moves intake work to Ready, but never resets active/blocked work. */
export async function readyColumnForBug(task: Task): Promise<string | undefined> {
  const { data: columns } = await api.listAllColumns(task.project_id);
  if (columns.find(column => column.id === task.column_id)?.semantic_state !== 'backlog') return undefined;
  const ready = columns.find(column => !column.archived_at && column.semantic_state === 'ready');
  if (!ready) throw new Error('This project has no Ready column. Restore one before accepting this bug.');
  return ready.id;
}
