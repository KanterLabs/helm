import type {
  AgentWorkStateFilter,
  BugResolution,
  BugSeverity,
  ReleaseFilterValue,
  ReleaseStatus,
  Task
} from '../types';

export interface TaskListParams {
  state?: string;
  column?: string;
  priority?: string;
  label?: string;
  assignee?: string;
  /** Project-local target release ID/name, or `unassigned`. */
  release?: ReleaseFilterValue;
  /** Compatibility alias; project task routes serialize this as `release`. */
  release_id?: ReleaseFilterValue;
  kind?: Task['kind'];
  severity?: BugSeverity | 'untriaged' | 'none';
  reporter?: string;
  resolution?: BugResolution | 'unresolved' | 'none';
  agent_state?: AgentWorkStateFilter;
  action_needed?: boolean | string;
  dependency?: 'blocked' | 'ready';
  q?: string;
  updated_after?: string;
  sort?: 'board' | 'position' | 'number' | 'created_at' | 'updated_at' | 'priority' | 'title' | string;
  order?: 'asc' | 'desc';
  cursor?: string;
  limit?: number;
}

export interface IssueListParams extends Omit<TaskListParams, 'kind' | 'release' | 'release_id'> {
  project?: string;
  /** Global issue route uses the stable `release_id` query parameter. */
  release_id?: ReleaseFilterValue;
  /** Accepted as a client-side alias and serialized to `release_id`. */
  release?: ReleaseFilterValue;
}

export type WorkView = 'assigned' | 'live';

export interface MyWorkParams {
  project?: string;
  state?: string;
  priority?: string;
  label?: string;
  /** Global My Work route uses the stable `release_id` query parameter. */
  release_id?: ReleaseFilterValue;
  /** Accepted as a client-side alias and serialized to `release_id`. */
  release?: ReleaseFilterValue;
  q?: string;
  updated_after?: string;
  view?: WorkView;
  agent_state?: AgentWorkStateFilter;
  action_needed?: boolean | string;
  dependency?: 'blocked' | 'ready';
  cursor?: string;
  limit?: number;
}

export interface SearchParams {
  clear?: string;
  q?: string;
  key?: string;
  title?: string;
  description?: string;
  label?: string;
  state?: string;
  priority?: string;
  assignee?: string;
  claim_owner?: string;
  /** Stable opaque release ID or the `unassigned` sentinel. */
  release_id?: ReleaseFilterValue;
  project?: string;
  due_from?: string;
  due_to?: string;
  sort?: string;
  view?: string;
  cursor?: string;
  limit?: number;
}

/** Cursor-paginated project release list filters. */
export interface ReleaseListParams {
  status?: ReleaseStatus;
  target_from?: string;
  target_to?: string;
  cursor?: string;
  limit?: number;
}

/** Cursor pagination for the dependency-aware release work queue. */
export interface ReleaseWorkQueueParams {
  cursor?: string;
  limit?: number;
}
