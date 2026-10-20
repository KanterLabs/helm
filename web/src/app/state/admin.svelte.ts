// Reactive state: Project administration form and confirmations.
import type { Column, Project, SemanticState } from '../../lib/types';
import type { AdminConfirmation } from '../types';

class AdminState {
  // Project administration intentionally lives alongside the existing
  // settings state. The board continues to consume only live columns while
  // this view keeps archived rows available for restore.
  adminProjects = $state.raw<Project[]>([]);
  adminProjectId = $state.raw('');
  adminProjectName = $state.raw('');
  adminProjectDescription = $state.raw('');
  adminProjectColor = $state.raw('#6d5efc');
  adminChecklistPolicy = $state.raw<'warn' | 'require'>('warn');
  adminColumns = $state.raw<Column[]>([]);
  adminLoading = $state.raw(false);
  adminSaving = $state.raw(false);
  adminColumnsLoading = $state.raw(false);
  adminColumnSaving = $state.raw('');
  adminError = $state.raw('');
  adminColumnName = $state.raw('');
  adminColumnState = $state.raw<SemanticState>('backlog');
  adminColumnCreating = $state.raw(false);
  adminConfirmation = $state.raw<AdminConfirmation | null>(null);
  adminConfirmationBusy = $state.raw(false);

  // Derived values
  adminProject: Project | undefined = $derived(admin.adminProjects.find((project) => project.id === admin.adminProjectId));
  adminLiveColumns: Column[] = $derived(admin.adminColumns.filter((column) => !column.archived_at).sort((a, b) => a.position - b.position));
  adminLiveColumnIndexes = $derived(new Map(admin.adminLiveColumns.map((column, index) => [column.id, index])));
}

export const admin = new AdminState();
