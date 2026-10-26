// Project administration: metadata, columns and confirmations.
import { api } from '../lib/api';
import type { Column, Project } from '../lib/types';
import { rememberDialogFocus, restoreDialogFocus } from './dialogs';
import { friendlyError, toast } from './feedback';
import { admin } from './state/admin.svelte';
import { board } from './state/board.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';
import type { AdminConfirmation } from './types';

export function setAdminProjectDraft(project: Project) {
  admin.adminProjectId = project.id;
  admin.adminProjectName = project.name;
  admin.adminProjectDescription = project.description || '';
  admin.adminProjectColor = project.color || '#64748b';
  admin.adminChecklistPolicy = project.checklist_completion_policy || 'warn';
}

export async function loadAdminColumns(projectId = admin.adminProjectId) {
  if (!projectId || !session.user?.admin) return;
  admin.adminColumnsLoading = true;
  try {
    admin.adminColumns = (await api.listAllColumns(projectId, { includeArchived: true })).data;
  } catch (error) {
    admin.adminError = friendlyError(error, 'Project columns could not be loaded.');
  } finally {
    admin.adminColumnsLoading = false;
  }
}

export async function loadProjectAdmin() {
  if (!session.user?.admin) return;
  admin.adminLoading = true;
  admin.adminError = '';
  try {
    const result = await api.listAllProjects({ includeArchived: true });
    admin.adminProjects = result.data;
    const selected = admin.adminProjects.find((project) => project.id === admin.adminProjectId)
      || admin.adminProjects.find((project) => project.id === workspace.activeProject?.id)
      || admin.adminProjects[0];
    if (!selected) {
      admin.adminProjectId = '';
      admin.adminColumns = [];
      return;
    }
    setAdminProjectDraft(selected);
    await loadAdminColumns(selected.id);
  } catch (error) {
    admin.adminError = friendlyError(error, 'Project administration could not be loaded.');
  } finally {
    admin.adminLoading = false;
  }
}

export function replaceAdminProject(updated: Project) {
  admin.adminProjects = admin.adminProjects.map((project) => project.id === updated.id ? updated : project);
  if (updated.archived_at) {
    workspace.projects = workspace.projects.filter((project) => project.id !== updated.id);
    if (workspace.activeProjectSlug === updated.slug) {
      workspace.activeProjectSlug = '';
      board.columns = [];
      board.tasks = [];
      board.labels = [];
    }
  } else if (!workspace.projects.some((project) => project.id === updated.id)) {
    workspace.projects = [...workspace.projects, updated];
  } else {
    workspace.projects = workspace.projects.map((project) => project.id === updated.id ? updated : project);
  }
  setAdminProjectDraft(updated);
}

export async function saveAdminProject() {
  const current = admin.adminProjects.find((project) => project.id === admin.adminProjectId);
  if (!current || !admin.adminProjectName.trim()) return;
  admin.adminSaving = true;
  admin.adminError = '';
  try {
    const updated = await api.patchProject(current.id, {
      name: admin.adminProjectName.trim(),
      description: admin.adminProjectDescription.trim(),
      color: admin.adminProjectColor,
      checklist_completion_policy: admin.adminChecklistPolicy
    }, current.version);
    replaceAdminProject(updated);
    toast('success', `${updated.name} settings saved.`);
  } catch (error) {
    admin.adminError = friendlyError(error, 'The project settings could not be saved. Refresh and try again.');
    await loadProjectAdmin();
    throw error;
  } finally {
    admin.adminSaving = false;
  }
}

export function confirmAdminProjectSave() {
  if (!admin.adminProject || !admin.adminProjectName.trim()) return;
  showAdminConfirmation({
    title: `Save ${admin.adminProjectName.trim()} settings?`,
    message: 'This updates the project name, description, accent color, and checklist completion policy. The stable project key and URL stay unchanged unless you edit them through the API.',
    confirmLabel: 'Save project',
    action: saveAdminProject
  });
}

export async function mutateAdminProjectArchive(project: Project) {
  const updated = await api.patchProject(project.id, { archived: !project.archived_at }, project.version);
  replaceAdminProject(updated);
  await loadAdminColumns(updated.id);
  toast('success', `${updated.name} was ${updated.archived_at ? 'archived' : 'restored'}.`);
}

export function confirmAdminProjectArchive(project: Project) {
  const archived = Boolean(project.archived_at);
  showAdminConfirmation({
    title: archived ? `Restore ${project.name}?` : `Archive ${project.name}?`,
    message: archived
      ? 'The project and its stable URL will be available in the workspace again.'
      : `${project.task_count ?? 0} task${project.task_count === 1 ? '' : 's'} will stay intact, but the project will leave the active workspace until restored.`,
    confirmLabel: archived ? 'Restore project' : 'Archive project',
    action: async () => {
      try {
        await mutateAdminProjectArchive(project);
      } catch (error) {
        await loadProjectAdmin();
        throw error;
      }
    }
  });
}

export function replaceAdminColumn(updated: Column) {
  admin.adminColumns = admin.adminColumns.map((column) => column.id === updated.id ? updated : column);
  if (updated.project_id === workspace.activeProject?.id) {
    board.columns = board.columns.map((column) => column.id === updated.id ? updated : column).filter((column) => !column.archived_at);
  }
}

export async function mutateAdminColumn(column: Column) {
  admin.adminColumnSaving = column.id;
  try {
    const updated = await api.patchColumn(column.id, {
      name: column.name.trim(),
      semantic_state: column.semantic_state
    }, column.version);
    replaceAdminColumn(updated);
    toast('success', `${updated.name} was saved.`);
  } finally {
    if (admin.adminColumnSaving === column.id) admin.adminColumnSaving = '';
  }
}

export function confirmAdminColumnSave(column: Column) {
  showAdminConfirmation({
    title: `Save ${column.name || 'column'}?`,
    message: 'Changing a semantic mapping may update task completion state and dependency readiness. The operation is transactional and can be retried if another admin edits it first.',
    confirmLabel: 'Save column',
    action: async () => {
      try {
        await mutateAdminColumn(column);
      } catch (error) {
        await loadAdminColumns();
        throw error;
      }
    }
  });
}

export async function mutateAdminColumnArchive(column: Column) {
  admin.adminColumnSaving = column.id;
  try {
    const updated = await api.patchColumn(column.id, { archived: !column.archived_at }, column.version);
    replaceAdminColumn(updated);
    await loadAdminColumns();
    toast('success', `${updated.name} was ${updated.archived_at ? 'archived' : 'restored'}.`);
  } finally {
    if (admin.adminColumnSaving === column.id) admin.adminColumnSaving = '';
  }
}

export function confirmAdminColumnArchive(column: Column) {
  const archived = Boolean(column.archived_at);
  showAdminConfirmation({
    title: archived ? `Restore ${column.name}?` : `Archive ${column.name}?`,
    message: archived
      ? 'Tasks can use this column again after it is restored.'
      : 'Tasks in this column will move to another live column with the same semantic state. The five required mappings remain available.',
    confirmLabel: archived ? 'Restore column' : 'Archive column',
    action: async () => {
      try {
        await mutateAdminColumnArchive(column);
      } catch (error) {
        await loadAdminColumns();
        throw error;
      }
    }
  });
}

export async function mutateAdminColumnMove(column: Column, position: number) {
  admin.adminColumnSaving = column.id;
  try {
    const updated = await api.patchColumn(column.id, { position }, column.version);
    replaceAdminColumn(updated);
    await loadAdminColumns();
    toast('success', `${updated.name} moved.`);
  } finally {
    if (admin.adminColumnSaving === column.id) admin.adminColumnSaving = '';
  }
}

export function confirmAdminColumnMove(column: Column, position: number) {
  showAdminConfirmation({
    title: `Move ${column.name}?`,
    message: 'Reordering changes the board for everyone. Existing tasks remain in their current column and keep their order.',
    confirmLabel: 'Move column',
    action: async () => {
      try {
        await mutateAdminColumnMove(column, position);
      } catch (error) {
        await loadAdminColumns();
        throw error;
      }
    }
  });
}

export async function mutateAdminColumnCreate() {
  const name = admin.adminColumnName.trim();
  if (!name) return;
  admin.adminColumnCreating = true;
  try {
    const created = await api.createColumn(admin.adminProjectId, { name, semantic_state: admin.adminColumnState });
    const firstArchived = admin.adminColumns.findIndex((column) => Boolean(column.archived_at));
    const insertAt = firstArchived === -1 ? admin.adminColumns.length : firstArchived;
    admin.adminColumns = [...admin.adminColumns.slice(0, insertAt), created, ...admin.adminColumns.slice(insertAt)];
    admin.adminColumnName = '';
    admin.adminColumnState = 'backlog';
    toast('success', `${created.name} was added.`);
  } finally {
    admin.adminColumnCreating = false;
  }
}

export function confirmAdminColumnCreate() {
  if (!admin.adminColumnName.trim() || !admin.adminProjectId) return;
  showAdminConfirmation({
    title: `Add ${admin.adminColumnName.trim()}?`,
    message: 'The new column will appear at the end of the board and use the selected semantic mapping.',
    confirmLabel: 'Add column',
    action: mutateAdminColumnCreate
  });
}

export function showAdminConfirmation(confirmation: AdminConfirmation) {
  rememberDialogFocus('[aria-labelledby="project-admin-heading"] button');
  admin.adminConfirmation = confirmation;
}

export async function runAdminConfirmation() {
  const pending = admin.adminConfirmation;
  if (!pending || admin.adminConfirmationBusy) return;
  admin.adminConfirmationBusy = true;
  admin.adminError = '';
  try {
    await pending.action();
    admin.adminConfirmation = null;
    restoreDialogFocus();
  } catch (error) {
    admin.adminError = friendlyError(error, 'The administration change could not be applied. Refresh and try again.');
  } finally {
    admin.adminConfirmationBusy = false;
  }
}

export function cancelAdminConfirmation() {
  admin.adminConfirmation = null;
  restoreDialogFocus();
  void loadAdminColumns();
}
