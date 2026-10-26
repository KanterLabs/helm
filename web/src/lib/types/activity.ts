import type { Actor, ActorKind, Project, SemanticState } from './core';
import type { AgentWorkState, Task } from './tasks';

export interface Comment {
  id: string;
  task_id: string;
  actor_id: string;
  body: string;
  /** Optimistic-concurrency version used by edit/delete mutations. */
  version?: number;
  created_at: string;
  updated_at: string;
  /** Tombstones are retained by the server but omitted from active reads. */
  deleted_at?: string;
  /** @deprecated The runtime response has no author object. */
  author?: Pick<Actor, 'name'> | string;
  /** @deprecated The runtime response has no actor object. */
  actor?: Pick<Actor, 'name'> | string;
}

export type AgentNoteCategory = 'known_issue' | 'rejected_approach' | 'constraint' | 'workaround';

export interface AgentNote {
  id: string;
  task_id: string;
  actor_id: string;
  category: AgentNoteCategory;
  body: string;
  evidence: string[];
  version: number;
  created_at: string;
  updated_at: string;
  resolved_at?: string;
}

export interface ActivityEvent {
  cursor: number;
  id: string;
  type: string;
  project_id?: string;
  task_id?: string;
  actor_id?: string;
  payload?: Record<string, unknown>;
  created_at: string;
  /** @deprecated These aliases are not emitted by the runtime event JSON. */
  kind?: never;
  action?: never;
  message?: never;
  actor?: Pick<Actor, 'name'> | string;
  metadata?: never;
}

/** A project- or task-scoped inbox watch owned by the current actor. */
export interface Watch {
  id: string;
  actor_id: string;
  project_id: string;
  task_id?: string | null;
  created_at: string;
}

/** Durable in-app notification emitted by an event or watched task change. */
export interface Notification {
  id: string;
  recipient_id: string;
  actor_id?: string | null;
  event_type: string;
  type?: string;
  project_id?: string | null;
  task_id?: string | null;
  title: string;
  body: string;
  payload: Record<string, unknown>;
  dedupe_key: string;
  read_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface NotificationPreferences {
  actor_id: string;
  assignments: boolean;
  mentions: boolean;
  blockers: boolean;
  state_changes: boolean;
  updated_at: string;
}

/** The discriminated payload kinds returned by the durable task timeline. */
export type TaskTimelineKind = 'agent_progress' | 'comment' | 'task_change';

export type TaskTimelineFilter = 'all' | TaskTimelineKind;

export interface TimelineActor {
  id: string;
  kind: ActorKind;
  name: string;
}

export interface TaskTimelineProgress {
  operation_id: string;
  actor_id: string;
  state: AgentWorkState;
  phase: string;
  summary: string;
  next_action: string;
  checkpoint_refs: string[];
  checkpoint_completed?: number | null;
  checkpoint_total?: number | null;
  started_at: string;
}

export interface TaskTimelineChange {
  event_id: string;
  event_type: string;
  payload: Record<string, unknown>;
}

/** One newest-first immutable activity row for a task. */
export interface TaskTimelineItem {
  id: string;
  cursor: string;
  kind: TaskTimelineKind;
  task_id: string;
  actor: TimelineActor | null;
  created_at: string;
  progress: TaskTimelineProgress | null;
  comment: Comment | null;
  change: TaskTimelineChange | null;
}

export interface TaskTimelineCollection {
  data: TaskTimelineItem[];
  next_cursor?: string | null;
}

/** The bounded activity filters exposed by the Roadmap follow-along view. */
export type RoadmapActivityFilter = 'all' | 'agent-updates' | 'comments' | 'task-changes';

export type RoadmapActivityKind = Exclude<RoadmapActivityFilter, 'all'>;

export interface RoadmapSummary {
  task_total: number;
  completed: number;
  completion_percent: number;
  state_counts: Record<SemanticState, number>;
  overdue: number;
  due_soon: number;
  upcoming: Task[];
  recent_activity: ActivityEvent[];
  project?: Project;
  projects?: RoadmapProject[];
  /** Optional aliases retained for the current UI and older payloads. */
  total_tasks?: number;
  completion_percentage?: number;
  completed_count?: number;
  overdue_count?: number;
  due_soon_count?: number;
  upcoming_tasks?: Task[];
}

export interface RoadmapProject {
  project: Project;
  total_tasks: number;
  completed_tasks: number;
  completion_percentage: number;
  state_counts?: Partial<Record<SemanticState, number>> & Record<string, number>;
}
