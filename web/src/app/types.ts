import type { api } from '../lib/api';
import type { BoardTaskSort } from '../lib/boardOrdering';
import type { CommandView } from '../lib/commandPalette';
import type { Column, Project, Task } from '../lib/types';
import type { ToastAccessibility } from '../lib/ui';

export type View = CommandView | 'releases' | 'admin' | 'tickets';

export type AuthView = 'login' | 'setup' | 'tailnet';

export type ToastKind = 'success' | 'error' | 'info';

export type ToastAction = {
    label: string;
    run: () => void | Promise<void>;
    pending?: boolean;
  };

export type ToastItem = { id: number; kind: ToastKind; message: string; action?: ToastAction } & ToastAccessibility;

export type ConfirmRequest = {
    title: string;
    message: string;
    confirmLabel: string;
    cancelLabel?: string;
    fallbackSelector?: string;
    resolve: (confirmed: boolean) => void;
  };

export type WorkFilter = 'all' | 'action-needed' | 'working' | 'waiting' | 'verifying' | 'stale' | 'handoff' | 'missing';

export type MyWorkView = 'live' | 'assigned';

export type CountStatus = 'unknown' | 'loading' | 'known' | 'error';

export type DrawerView = 'details' | 'activity';

export type MyWorkRow = { task: Task; project?: Project; column?: Column };

export type TaskModalSuggestionField = 'title' | 'description' | 'priority';

export type TaskModalAppliedFields = Record<TaskModalSuggestionField, boolean>;

export type DialogReturnFocus = { element: HTMLElement | null; fallbackSelector: string };

/**
* Release shapes stay local to the shell so the page can render while a
* retained API/types bundle is being upgraded. The release client supplies
* the same wire fields once available.
*/
export type ReleaseSummaryModel = {
    task_count?: number;
    completed_count?: number;
    blocked_count?: number;
    claimed_count?: number;
    checklist_warning_count?: number;
    required_task_count?: number;
    required_completed_count?: number;
    cross_release_conflict_count?: number;
    ready_to_release?: boolean;
  };

export type ReleaseModel = {
    id: string;
    project_id: string;
    name: string;
    description?: string;
    target_date?: string | null;
    status: 'planned' | 'released' | string;
    released_at?: string | null;
    released_by?: string | null;
    version: number;
    created_at?: string;
    updated_at?: string;
    summary?: ReleaseSummaryModel;
  };

export type ReleaseQueueItem = {
    task?: { id?: string; key?: string; version?: number };
    relationship?: string;
    disposition?: string;
    blocked_by?: Array<{ id?: string; key?: string; title?: string }>;
  };

export type ReleaseQueueModel = {
    release?: { id?: string; name?: string; version?: number };
    snapshot?: { project_revision?: number; read_at?: string };
    summary?: {
      direct?: number;
      required?: number;
      completed?: number;
      claimable?: number;
      owned?: number;
      dependency_blocked?: number;
      manually_blocked?: number;
      claimed_elsewhere?: number;
      cross_release_conflicts?: number;
    };
    data?: ReleaseQueueItem[];
    next_cursor?: string;
  };

export type OpenTaskOptions = {
    skipDiscardGuard?: boolean;
    returnFocus?: DialogReturnFocus | null;
  };

export type AdminConfirmation = {
    title: string;
    message: string;
    confirmLabel: string;
    action: () => Promise<void>;
  };

export type BoardSort = BoardTaskSort;

export type BoardColumnPage = {
    tasks: Task[];
    nextCursor: string;
    loading: boolean;
    loaded: boolean;
    error: string;
  };

export type BoardLoadOptions = { criteriaRevision?: number; background?: boolean };

export type BoardColumnLoadOptions = {
    reset?: boolean;
    mutationSnapshot?: number;
    recoveryNotice?: boolean;
    announceChanges?: boolean;
  };

export type BoardMutationContext = {
    requestId: number;
    boardRequest: number;
    session: number;
    mutationRevision: number;
    projectId: string;
    projectSlug: string;
  };

export type TaskCreation = { projectId: string; input: Parameters<typeof api.createTask>[1]; key: string };

export type TaskWithRelease = Task & {
    release_id?: string | null;
    release?: { id?: string; name?: string; status?: string; target_date?: string | null } | null;
  };

export type BetaBuildLoadOptions = {
    initial?: boolean;
    silent?: boolean;
    preserveEnabled?: boolean;
  };
