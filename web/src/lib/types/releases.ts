/** Product-planning release lifecycle state. This is unrelated to releasing a task claim. */
export type ReleaseStatus = 'planned' | 'released';

/** Stable sentinel used by collection filters for tasks without a release. */
export const UNASSIGNED_RELEASE = 'unassigned' as const;

export type ReleaseFilterValue = string | typeof UNASSIGNED_RELEASE;

/** Compatibility alias for callers that treat a release filter as a domain type. */
export type ReleaseFilter = ReleaseFilterValue;

/** Direct membership and transitive prerequisite progress for a release. */
export interface ReleaseSummary {
  task_count: number;
  completed_count: number;
  blocked_count: number;
  claimed_count: number;
  checklist_warning_count: number;
  required_task_count: number;
  required_completed_count: number;
  cross_release_conflict_count: number;
  ready_to_release: boolean;
}

/** Compact project-local release relation embedded on task reads. */
export interface ReleaseReference {
  id: string;
  name: string;
  status: ReleaseStatus;
  /** Omitted by the API when no target date is set. */
  target_date?: string | null;
}

/** Compatibility alias for callers that use the shorter relation name. */
export type ReleaseRef = ReleaseReference;

/** Project-local planned or released product delivery boundary. */
export interface Release {
  id: string;
  project_id: string;
  name: string;
  description: string;
  target_date?: string | null;
  status: ReleaseStatus;
  released_at?: string | null;
  released_by?: string | null;
  version: number;
  created_at: string;
  updated_at: string;
  summary: ReleaseSummary;
}

/** Inputs accepted by the create-release endpoint. */
export interface ReleaseCreateInput {
  name: string;
  description?: string | null;
  target_date?: string | null;
}

/** Inputs accepted by the release metadata PATCH endpoint. */
export type ReleasePatchInput = Partial<Pick<ReleaseCreateInput, 'name' | 'description' | 'target_date'>>;

/** Required reason for reopening a released release. */
export interface ReleaseReopenInput {
  reason: string;
}

/** Compatibility aliases matching the OpenAPI request schema names. */
export type ReleaseCreateRequest = ReleaseCreateInput;

export type ReleasePatchRequest = ReleasePatchInput;

export type ReleaseReopenRequest = ReleaseReopenInput;

/** Compact task identity/version returned by the release work queue. */
export interface ReleaseQueueTaskReference {
  id: string;
  key: string;
  version: number;
  /** Present in some contract revisions; the queue's release already scopes it. */
  project_id?: string;
}

/** Compact prerequisite reference that explains why a queue item is blocked. */
export interface ReleaseQueueBlocker {
  id: string;
  key: string;
  title: string;
  completed_at: string | null;
  satisfied: boolean;
}

export type ReleaseQueueRelationship = 'direct' | 'prerequisite';

export type ReleaseQueueDisposition =
  | 'completed'
  | 'owned'
  | 'claimable'
  | 'dependency_blocked'
  | 'manually_blocked'
  | 'claimed_elsewhere'
  | 'cross_release_conflict';

/** One direct member or transitive prerequisite in a release work queue. */
export interface ReleaseQueueItem {
  task: ReleaseQueueTaskReference;
  relationship: ReleaseQueueRelationship;
  disposition: ReleaseQueueDisposition;
  blocked_by: ReleaseQueueBlocker[];
}

/** Snapshot metadata captured for one queue read. */
export interface ReleaseQueueSnapshot {
  project_revision: number;
  task_collection_revision?: number;
  read_at: string;
}

/** Aggregate disposition counts for one release work-queue snapshot. */
export interface ReleaseQueueSummary {
  direct: number;
  required: number;
  completed: number;
  claimable: number;
  owned: number;
  dependency_blocked: number;
  manually_blocked: number;
  claimed_elsewhere: number;
  cross_release_conflicts: number;
}

/** Compact release identity/version echoed by the queue response. */
export interface ReleaseQueueRelease {
  id: string;
  name: string;
  version: number;
}

/** Bounded, cursor-paginated, dependency-aware release work queue. */
export interface ReleaseWorkQueue {
  release: ReleaseQueueRelease;
  snapshot: ReleaseQueueSnapshot;
  summary: ReleaseQueueSummary;
  data: ReleaseQueueItem[];
  next_cursor: string;
}

/** Verbose aliases used by some consumers of the work-queue API. */
export type ReleaseQueue = ReleaseWorkQueue;

export type ReleaseQueueTask = ReleaseQueueTaskReference;

export type ReleaseWorkQueueTaskReference = ReleaseQueueTaskReference;

export type ReleaseWorkQueueBlocker = ReleaseQueueBlocker;

export type ReleaseWorkQueueItem = ReleaseQueueItem;

export type ReleaseWorkQueueSnapshot = ReleaseQueueSnapshot;

export type ReleaseWorkQueueSummary = ReleaseQueueSummary;
