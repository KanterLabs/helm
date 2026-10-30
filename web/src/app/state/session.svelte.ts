// Reactive state: Session, authentication and unsaved-work guards.
import { fromStore } from 'svelte/store';
import { pendingMutationCount } from '../../lib/api';
import { isAdmissionPending, isWorkspaceAdmin } from '../../lib/components/membership/access';
import type { Actor, AuthStatus } from '../../lib/types';
import type { AuthView } from '../types';
import { admin } from './admin.svelte';
import { board } from './board.svelte';
import { bugModal } from './bugModal.svelte';
import { bulk } from './bulk.svelte';
import { creation } from './creation.svelte';
import { drawer } from './drawer.svelte';
import { portable } from './portable.svelte';
import { releasePlan } from './releasePlan.svelte';
import { search } from './search.svelte';
import { settings } from './settings.svelte';
import { taskModal } from './taskModal.svelte';
import { workspace } from './workspace.svelte';

const pendingMutationCountStore = fromStore(pendingMutationCount);

class SessionState {
  booting = $state.raw(true);
  authStatus = $state.raw<AuthStatus | null>(null);
  user = $state.raw<Actor | null>(null);
  authView = $state.raw<AuthView>('login');
  authSubmitting = $state.raw(false);
  authError = $state.raw('');
  authBootstrapFailed = $state.raw(false);
  bootstrapController = $state.raw<AbortController | undefined>();
  bootstrapRequest = $state.raw(0);
  loginEmail = $state.raw('');
  loginPassword = $state.raw('');
  setupName = $state.raw('');
  setupEmail = $state.raw('');
  setupPassword = $state.raw('');
  sessionGeneration = $state.raw(0);
  pwaReloadApproved = $state.raw(false);
  beforeUnloadAttached = $state.raw(false);
  authenticatedActorId = $state.raw('');

  // Derived values
  /** Verified human awaiting owner/admin admission: no workspace UI or reads. */
  admissionPending = $derived(isAdmissionPending(this.authStatus, this.user));
  /** Active human owner/admin, or the development auth-disabled bypass. */
  workspaceAdmin = $derived(isWorkspaceAdmin(this.authStatus, this.user));
  updateBlocked = $derived(Boolean(pendingMutationCountStore.current || creation.projectCreating || taskModal.taskModalCreating || bugModal.bugModalCreating || creation.quickAddCreating
    || creation.projectCreation || creation.taskCreation || creation.bugCreation || creation.quickCreation || drawer.drawerSaving || drawer.commentSending
    || drawer.editedCommentPending || drawer.checklistPending || drawer.taskActionLoading || releasePlan.releaseModalSaving || admin.adminSaving || admin.adminColumnSaving
    || admin.adminColumnCreating || settings.tokenCreating || settings.agentSaving || portable.portableBusy || bulk.bulkSubmitting || search.savedViewSaving));
  protectedDrafts = $derived(drawer.drawerDraftDirty || drawer.editedCommentDirty || drawer.checklistDirty || workspace.ticketsDirty || Boolean(
    taskModal.showTaskModal && (taskModal.taskModalTitle || taskModal.taskModalDescription || taskModal.taskModalIdea || taskModal.taskModalDueDate || taskModal.taskModalAssignee || taskModal.taskModalPriority !== 'normal' || taskModal.taskModalReleaseId)
    || bugModal.showBugModal && (bugModal.bugModalTitle || bugModal.bugModalDescription || bugModal.bugModalActual || bugModal.bugModalExpected || bugModal.bugModalReproduction || bugModal.bugModalEnvironment || bugModal.bugModalVersion || bugModal.bugModalLabels || bugModal.bugModalSeverity || bugModal.bugModalPriority !== 'normal' || bugModal.bugModalReleaseId)
    || creation.showProjectModal && (creation.projectNameDraft || creation.projectKeyDraft || creation.projectDescriptionDraft || creation.projectColorDraft !== '#6d5efc')
    || Object.values(board.quickAddTitle).some((value) => value.trim())
    || releasePlan.showReleaseModal && (releasePlan.releaseModalName || releasePlan.releaseModalDescription || releasePlan.releaseModalTargetDate)
    || workspace.view === 'settings' && (settings.agentNameDraft || settings.agentDescriptionDraft || settings.tokenNameDraft || admin.adminColumnName
      || admin.adminProject && (admin.adminProjectName !== admin.adminProject.name || admin.adminProjectDescription !== (admin.adminProject.description || '') || admin.adminProjectColor !== (admin.adminProject.color || '#64748b') || admin.adminChecklistPolicy !== (admin.adminProject.checklist_completion_policy || 'warn')))
  ));
}

export const session = new SessionState();
