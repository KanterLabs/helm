import { requestScope } from '../lib/components/audit/columns/scopeIntent';
import { api } from '../lib/api';
import { openTask } from './drawer/open';
import { friendlyError, toast } from './feedback';
import { setView } from './routing';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';

/** Board entry points open the project's Audit columns scope; nothing starts yet. */
export async function openColumnAudit(taskIds: string[] = []): Promise<void> {
  const project = workspace.activeProject;
  if (!project) return;
  requestScope(project.id, taskIds);
  await setView('audits');
}

/** A late off-page lookup must not reopen a card in another session/project. */
export async function openAuditedTask(taskId: string): Promise<void> {
  const projectId = workspace.activeProject?.id;
  const generation = session.sessionGeneration;
  const current = () => generation === session.sessionGeneration && projectId === workspace.activeProject?.id;
  try {
    const task = await api.getTask(taskId);
    if (current() && task.project_id === projectId) openTask(task, 'details');
  } catch (error) {
    if (current()) toast('error', friendlyError(error, 'This card could not be opened.'));
  }
}
