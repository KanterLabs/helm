import type { Actor, ChecklistCompletionPolicy, Label, Priority, SemanticState } from './core';
import type { AlertSource, TicketMembership } from './tickets';
import type { ReleaseReference } from './releases';

export type TaskKind = 'task' | 'bug';

export type BugSeverity = 's1' | 's2' | 's3' | 's4';

export type BugResolution = 'fixed' | 'duplicate' | 'not_planned' | 'cannot_reproduce' | 'works_as_designed';

/** States published by an agent while it is actively working a task. */
export type AgentWorkState = 'working' | 'waiting' | 'verifying' | 'handoff';

/** Published states plus server-derived collection filters. */
export type AgentWorkStateFilter = AgentWorkState | 'stale' | 'missing';

/**
 * The server's current, structured progress for an agent-owned task.
 *
 * `stale` and `action_needed` are response-time conveniences supplied by the
 * API. Consumers should still derive them from `updated_at` when rendering a
 * long-lived response so the displayed state does not age silently.
 */
export interface AgentWork {
  operation_id: string;
  actor_id: string;
  state: AgentWorkState;
  phase?: string;
  summary: string;
  next_action?: string;
  checkpoint_refs: string[];
  checkpoint_completed?: number;
  checkpoint_total?: number;
  started_at: string;
  updated_at: string;
  stale: boolean;
  action_needed: boolean;
}

/** Fields accepted when an agent publishes or refreshes task work. */
export interface AgentWorkInput {
  operation_id: string;
  state: AgentWorkState;
  phase?: string;
  summary: string;
  next_action?: string;
  checkpoint_refs?: string[];
  checkpoint_completed?: number;
  checkpoint_total?: number;
}

/** More explicit alias for callers that prefer the endpoint's action name. */
export type PublishAgentWorkInput = AgentWorkInput;

/** Runtime task JSON uses actor IDs; the partial shape keeps old board fixtures compatible. */
export type TaskActorReference = string | Pick<Actor, 'id' | 'kind' | 'name'>;

/** Bug metadata is nested under a task in the runtime API response. */
export interface BugDetails {
  reporter_id: string;
  severity?: BugSeverity | null;
  actual_behavior: string;
  expected_behavior: string;
  reproduction_steps: string;
  environment: string;
  affected_version: string;
  resolution?: BugResolution | null;
  resolved_by?: string | null;
  resolved_at?: string | null;
  duplicate_of?: string | null;
}

/** Fields accepted when creating or patching nested bug metadata. */
export type BugInput = Partial<Pick<BugDetails, 'severity' | 'expected_behavior' | 'reproduction_steps' | 'environment' | 'affected_version'>> & {
  actual_behavior: string;
};

export type BugPatch = Partial<Pick<BugDetails, 'severity' | 'actual_behavior' | 'expected_behavior' | 'reproduction_steps' | 'environment' | 'affected_version'>>;

export interface TriageInput {
  severity: BugSeverity;
  priority?: Priority;
  assignee?: string | null;
  column_id?: string | null;
}

export interface ResolveInput {
  resolution: BugResolution;
  duplicate_of?: string | null;
  note?: string;
}

export interface ReopenInput {
  reason: string;
}

/** Derived direct-edge counts embedded in task collections and detail reads. */
export interface DependencySummary {
  prerequisite_count: number;
  unmet_prerequisite_count: number;
  dependent_count: number;
  blocked: boolean;
}

/** One bounded task reference returned by the dependency graph endpoint. */
export interface TaskReference {
  id: string;
  key: string;
  title: string;
  completed_at: string | null;
  satisfied: boolean;
}

/** Direct dependency edges in both directions for one task. */
export interface TaskDependencies {
  prerequisites: TaskReference[];
  dependents: TaskReference[];
}

export interface TaskChecklistItem {
  id: string;
  task_id: string;
  text: string;
  position: number;
  completed: boolean;
  completed_at?: string | null;
  completed_by?: string | null;
  created_at?: string;
  updated_at?: string;
}

export interface TaskChecklistSummary {
  total: number;
  completed: number;
  open: number;
  percent: number;
  completion_policy: ChecklistCompletionPolicy;
  warning: boolean;
}

export interface TaskChecklistCollection {
  task_id: string;
  version: number;
  data: TaskChecklistItem[];
  summary: TaskChecklistSummary;
}

export interface TaskChecklistItemInput {
  text: string;
  completed?: boolean;
  position?: number;
}

export interface TaskChecklistItemPatch {
  text?: string;
  completed?: boolean;
  position?: number;
}

/** A bounded parent/child relation reference returned by hierarchy reads. */
export interface TaskHierarchyReference {
  id: string;
  number: number;
  key: string;
  project_id: string;
  title: string;
  kind?: TaskKind;
  column_id: string;
  semantic_state: SemanticState | string;
  state?: SemanticState | string;
  version: number;
  parent_id?: string | null;
  completed_at?: string | null;
  agent_work?: AgentWork | null;
}

/** Server-derived direct-child rollups. */
export interface HierarchySummary {
  child_count: number;
  completed_child_count: number;
  completion_percent: number;
  state_counts: Record<string, number>;
  blocked_child_count: number;
  live_agent_work_count: number;
  action_needed_count: number;
  stale_agent_work_count: number;
}

export interface TaskHierarchy {
  parent?: TaskHierarchyReference | null;
  children: TaskHierarchyReference[];
  ancestors: TaskHierarchyReference[];
  descendants: TaskHierarchyReference[];
  summary: HierarchySummary;
}

export interface Task {
  id: string;
  number: number;
  key: string;
  project_id: string;
  column_id: string;
  title: string;
  description?: string;
  /** Existing task responses may omit kind while older servers are upgraded. */
  kind?: TaskKind;
  bug?: BugDetails | null;
  priority: Priority;
  position: number;
  /** Runtime task JSON uses actor IDs and omits unset references. */
  assignee?: TaskActorReference;
  claimed_by?: TaskActorReference;
  claim_expires_at?: string;
  due_at?: string;
  /** Explicit nullable project-local delivery release assignment. */
  release_id?: string | null;
  /** Compact release relation; omitted when the task is unassigned. */
  release?: ReleaseReference | null;
  version: number;
  created_at?: string;
  updated_at?: string;
  completed_at?: string;
  labels?: Label[];
  comment_count?: number;
  /** Structured progress published by the current agent, when present. */
  agent_work?: AgentWork | null;
  /** Present on current servers; optional keeps retained-server fixtures usable. */
  dependency_summary?: DependencySummary;
  /** Ordered acceptance criteria; omitted by retained-server fixtures. */
  checklist?: TaskChecklistItem[];
  checklist_summary?: TaskChecklistSummary;
  parent_task_id?: string | null;
  parent_id?: string | null;
  parent?: TaskHierarchyReference | null;
  hierarchy_summary?: HierarchySummary;
  /** Read-only external alert evidence; omitted for ordinary tasks. */
  alert_source?: AlertSource;
  /** Tickets workspace membership; omitted for ordinary tasks. */
  ticket?: TicketMembership;
}

export type TaskRouteIntent = 'details' | 'activity';

export type TaskPatch = Partial<Pick<Task, 'title' | 'description' | 'priority' | 'column_id' | 'position'>> & {
  kind?: TaskKind;
  bug?: BugPatch | null;
  description?: string | null;
  due_at?: string | null;
  assignee?: string | null;
  labels?: string[] | null;
  label_ids?: string[] | null;
  parent?: string | null;
  parent_id?: string | null;
  parent_task_id?: string | null;
  /** Omit to preserve the assignment; pass null to clear it. */
  release_id?: string | null;
};

/** A bounded, optimistic-concurrency guarded project task mutation. */
export type BulkTaskMutationMode = 'partial' | 'atomic';

export type BulkTaskMutationOperation = 'move' | 'assign' | 'priority' | 'labels' | 'due_at' | 'complete' | 'block';

export interface BulkTaskMutationInput {
  task: string;
  version: number;
  operation: BulkTaskMutationOperation;
  destination_column_id?: string;
  expected_source_column_id?: string;
  source?: string;
  reason?: string;
  comment?: string;
  assignee?: string | null;
  priority?: Priority;
  labels?: string[] | null;
  due_at?: string | null;
}

export interface BulkTaskMutationRequest {
  mode: BulkTaskMutationMode;
  mutations: BulkTaskMutationInput[];
}

export type BulkTaskMutationResultStatus = 'applied' | 'skipped' | 'conflict';

export interface BulkTaskMutationResult {
  reference: string;
  task_id?: string;
  status: BulkTaskMutationResultStatus;
  version?: number;
  task?: Task;
  error?: {
    code: string;
    message: string;
    status?: number;
    details?: Record<string, unknown>;
  };
}

export interface BulkTaskMutationResponse {
  mode: BulkTaskMutationMode;
  status: 'partial' | 'complete' | 'failed';
  requested: number;
  applied: number;
  skipped: number;
  conflicts: number;
  results: BulkTaskMutationResult[];
}

/** Explicit task reconciliation or precise board placement intent. */
export interface TaskMoveInput {
  destination_column_id: string;
  expected_source_column_id: string;
  source: string;
  reason?: string;
  before_task_id?: string;
  after_task_id?: string;
  placement?: 'first' | 'before' | 'between' | 'after' | 'last';
  expected_ordering_version?: number;
  expected_source_ordering_version?: number;
  expected_destination_ordering_version?: number;
  /** @deprecated The server allocates positions; use anchors instead. */
  position?: number;
}

/** Precise card placement intent used by the board drag and keyboard paths. */
export type TaskReorderInput = TaskMoveInput;
