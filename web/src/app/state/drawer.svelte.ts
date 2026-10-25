// Reactive state: Task drawer: task, drafts, activity timeline and pending actions.
import type { BugResolution, BugSeverity, Priority, Task, TaskRouteIntent, TaskTimelineFilter, TaskTimelineItem } from '../../lib/types';
import type { DrawerView } from '../types';

class DrawerState {
  taskDetailRequest = $state.raw(0);
  drawerTask = $state.raw<Task | null>(null);
  editedCommentDirty = $state.raw(false);
  editedCommentPending = $state.raw(false);
  checklistDirty = $state.raw(false);
  checklistPending = $state.raw(false);
  drawerDependencyPanel = $state.raw<{ refreshRelationships: () => Promise<boolean> } | null>(null);
  drawerDependencyRefresh = $state.raw(0);
  drawerHierarchyPanel = $state.raw<{ refreshRelationships: () => Promise<boolean> } | null>(null);
  drawerHierarchyRefresh = $state.raw(0);
  // The route intent is kept separate from drawer rendering so the drawer can
  // add its Activity tab without changing dashboard link semantics.
  taskRouteIntent = $state.raw<TaskRouteIntent>('details');
  taskRouteOrigin = $state.raw('');
  drawerView = $state.raw<DrawerView>('details');
  drawerLoading = $state.raw(false);
  drawerSaving = $state.raw(false);
  drawerError = $state.raw('');
  commentBody = $state.raw('');
  commentSending = $state.raw(false);
  drawerTimelineItems = $state.raw<TaskTimelineItem[]>([]);
  drawerTimelineNextCursor = $state.raw('');
  drawerTimelineLoading = $state.raw(false);
  drawerTimelineLoadingOlder = $state.raw(false);
  drawerTimelineError = $state.raw('');
  drawerTimelineFilter = $state.raw<TaskTimelineFilter>('all');
  drawerTimelineTaskId = $state.raw('');
  drawerTimelineRequest = $state.raw(0);
  draftTitle = $state.raw('');
  draftDescription = $state.raw('');
  draftPriority = $state.raw<Priority>('normal');
  draftDueDate = $state.raw('');
  draftAssignee = $state.raw('');
  draftReleaseId = $state.raw('');
  draftLabels = $state.raw('');
  draftBugActual = $state.raw('');
  draftBugExpected = $state.raw('');
  draftBugReproduction = $state.raw('');
  draftBugEnvironment = $state.raw('');
  draftBugVersion = $state.raw('');
  triageSeverityDraft = $state.raw<BugSeverity>('s3');
  resolutionDraft = $state.raw<BugResolution>('fixed');
  duplicateOfDraft = $state.raw('');
  resolutionNoteDraft = $state.raw('');
  reopenReasonDraft = $state.raw('');
  blockReasonDraft = $state.raw('');
  blockReasonOpen = $state.raw(false);
  // Keep the drawer's saved baseline separate from the authoritative task
  // model. Background refreshes may update the latter without replacing the
  // fields a person is currently editing.
  drawerSavedTaskDraftFingerprint = $state.raw('');
  drawerSavedActionDraftFingerprint = $state.raw('');
  taskActionLoading = $state.raw('');
  labelDeleting = $state.raw('');

  // Derived values
  // Keep these reactive expressions explicit: Svelte tracks identifiers in a
  // statement, not values reached indirectly through a helper function.
  drawerTaskDraftFingerprintValue = $derived(JSON.stringify([
    drawer.draftTitle,
    drawer.draftDescription,
    drawer.draftPriority,
    drawer.draftDueDate,
    drawer.draftAssignee,
    drawer.draftReleaseId,
    drawer.draftLabels,
    drawer.draftBugActual,
    drawer.draftBugExpected,
    drawer.draftBugReproduction,
    drawer.draftBugEnvironment,
    drawer.draftBugVersion
  ]));
  drawerActionDraftFingerprintValue = $derived(JSON.stringify([
    drawer.triageSeverityDraft,
    drawer.resolutionDraft,
    drawer.duplicateOfDraft,
    drawer.resolutionNoteDraft,
    drawer.reopenReasonDraft,
    drawer.blockReasonDraft,
    drawer.commentBody
  ]));
  drawerTaskDraftDirty = $derived(Boolean(drawer.drawerTask && drawer.drawerTaskDraftFingerprintValue !== drawer.drawerSavedTaskDraftFingerprint));
  drawerDraftDirty = $derived(Boolean(
    drawer.drawerTask
    && (drawer.drawerTaskDraftDirty || drawer.drawerActionDraftFingerprintValue !== drawer.drawerSavedActionDraftFingerprint)
  ));
}

export const drawer = new DrawerState();
