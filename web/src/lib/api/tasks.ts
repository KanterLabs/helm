import type {
  AgentWorkInput,
  BugInput,
  BulkTaskMutationRequest,
  BulkTaskMutationResponse,
  Collection,
  ReopenInput,
  ResolveInput,
  Task,
  TaskChecklistCollection,
  TaskChecklistItemInput,
  TaskChecklistItemPatch,
  TaskDependencies,
  TaskHierarchy,
  TaskHierarchyReference,
  TaskMoveInput,
  TaskPatch,
  TaskReorderInput,
  TriageInput
} from '../types';
import type { IssueListParams, TaskListParams } from './params';
import { collectionFrom, key, pathWithQuery, request } from './client';

interface TaskDelegates {
  updateTaskChecklistItem(task: string, item: string, input: TaskChecklistItemPatch, version: number): Promise<Task>;
}

export function createTasksApi(getApi: () => TaskDelegates) {
  return {
    listTasks: (project: string, params: TaskListParams = {}) =>
      request<Collection<Task> | Task[]>(
        pathWithQuery(`/projects/${encodeURIComponent(project)}/tasks`, {
          state: params.state,
          column: params.column,
          priority: params.priority,
          label: params.label,
          assignee: params.assignee,
          release: params.release ?? params.release_id,
          kind: params.kind,
          severity: params.severity,
          reporter: params.reporter,
          resolution: params.resolution,
          agent_state: params.agent_state,
          action_needed: params.action_needed,
          dependency: params.dependency,
          q: params.q,
          updated_after: params.updated_after,
          sort: params.sort,
          order: params.order,
          cursor: params.cursor,
          limit: params.limit ?? 200
        })
      ).then(collectionFrom),

    listIssues: (params: IssueListParams = {}) =>
      request<Collection<Task> | Task[]>(
        pathWithQuery('/issues', {
          project: params.project,
          state: params.state,
          column: params.column,
          priority: params.priority,
          label: params.label,
          assignee: params.assignee,
          release_id: params.release_id ?? params.release,
          severity: params.severity,
          reporter: params.reporter,
          resolution: params.resolution,
          agent_state: params.agent_state,
          action_needed: params.action_needed,
          dependency: params.dependency,
          q: params.q,
          updated_after: params.updated_after,
          cursor: params.cursor,
          limit: params.limit ?? 200
        })
      ).then(collectionFrom),

    createTask: (
      project: string,
      input: {
        title: string;
        description?: string;
        priority?: Task['priority'];
        kind?: Task['kind'];
        bug?: BugInput;
        column_id?: string;
        position?: number;
        due_at?: string | null;
        assignee?: string | null;
        /** Omit for no assignment; pass null to leave the new task unassigned. */
        release_id?: string | null;
        labels?: string[];
        label_ids?: string[];
        parent?: string | null;
        parent_id?: string | null;
        parent_task_id?: string | null;
      },
      idempotencyKey = key()
    ) =>
      request<Task>(`/projects/${encodeURIComponent(project)}/tasks`, {
        method: 'POST',
        body: input,
        idempotencyKey
      }),

    getTask: (task: string) => request<Task>(`/tasks/${encodeURIComponent(task)}`),

    patchTask: (task: string, input: TaskPatch, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}`, {
        method: 'PATCH',
        body: input,
        ifMatch: version,
        idempotencyKey: key()
      }),

    bulkTasks: (project: string, input: BulkTaskMutationRequest) =>
      request<BulkTaskMutationResponse>(`/projects/${encodeURIComponent(project)}/tasks/bulk`, {
        method: 'POST',
        body: input,
        idempotencyKey: key()
      }),

    getTaskDependencies: (task: string) =>
      request<TaskDependencies>(`/tasks/${encodeURIComponent(task)}/dependencies`),

    getTaskChecklist: (task: string) =>
      request<TaskChecklistCollection>(`/tasks/${encodeURIComponent(task)}/checklist`),

    addTaskChecklistItem: (task: string, input: TaskChecklistItemInput, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/checklist`, {
        method: 'POST',
        body: input,
        ifMatch: version,
        idempotencyKey: key()
      }),

    updateTaskChecklistItem: (task: string, item: string, input: TaskChecklistItemPatch, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/checklist/${encodeURIComponent(item)}`, {
        method: 'PATCH',
        body: input,
        ifMatch: version,
        idempotencyKey: key()
      }),

    patchTaskChecklistItem: (task: string, item: string, input: TaskChecklistItemPatch, version: number) =>
      getApi().updateTaskChecklistItem(task, item, input, version),

    deleteTaskChecklistItem: (task: string, item: string, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/checklist/${encodeURIComponent(item)}`, {
        method: 'DELETE',
        ifMatch: version,
        idempotencyKey: key()
      }),

    reorderTaskChecklist: (task: string, itemIds: string[], version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/checklist`, {
        method: 'PATCH',
        body: { item_ids: itemIds },
        ifMatch: version,
        idempotencyKey: key()
      }),

    addTaskDependency: (task: string, prerequisite: string, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/dependencies`, {
        method: 'POST',
        body: { prerequisite },
        ifMatch: version,
        idempotencyKey: key()
      }),

    removeTaskDependency: (task: string, prerequisite: string, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/dependencies/${encodeURIComponent(prerequisite)}`, {
        method: 'DELETE',
        ifMatch: version,
        idempotencyKey: key()
      }),

    getTaskHierarchy: (task: string) =>
      request<TaskHierarchy>(`/tasks/${encodeURIComponent(task)}/hierarchy`),

    listTaskChildren: (task: string) =>
      request<Collection<TaskHierarchyReference> | TaskHierarchyReference[]>(`/tasks/${encodeURIComponent(task)}/children`).then(collectionFrom),

    listTaskAncestors: (task: string) =>
      request<Collection<TaskHierarchyReference> | TaskHierarchyReference[]>(`/tasks/${encodeURIComponent(task)}/ancestors`).then(collectionFrom),

    listTaskDescendants: (task: string) =>
      request<Collection<TaskHierarchyReference> | TaskHierarchyReference[]>(`/tasks/${encodeURIComponent(task)}/descendants`).then(collectionFrom),

    setTaskParent: (task: string, parent: string, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/parent`, {
        method: 'POST',
        body: { parent },
        ifMatch: version,
        idempotencyKey: key()
      }),

    clearTaskParent: (task: string, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/parent`, {
        method: 'DELETE',
        ifMatch: version,
        idempotencyKey: key()
      }),

    removeTaskChild: (parent: string, child: string, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(parent)}/children/${encodeURIComponent(child)}`, {
        method: 'DELETE',
        ifMatch: version,
        idempotencyKey: key()
      }),

    triageTask: (task: string, version: number, input: TriageInput) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/triage`, {
        method: 'POST',
        body: input,
        ifMatch: version,
        idempotencyKey: key()
      }),

    resolveTask: (task: string, version: number, input: ResolveInput) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/resolve`, {
        method: 'POST',
        body: input,
        ifMatch: version,
        idempotencyKey: key()
      }),

    reopenTask: (task: string, version: number, input: ReopenInput) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/reopen`, {
        method: 'POST',
        body: input,
        ifMatch: version,
        idempotencyKey: key()
      }),

    deleteTask: (task: string, version: number) =>
      request<void>(`/tasks/${encodeURIComponent(task)}`, {
        method: 'DELETE',
        ifMatch: version,
        idempotencyKey: key()
      }),

    restoreTask: (task: string, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/restore`, {
        method: 'POST',
        ifMatch: version,
        idempotencyKey: key()
      }),

    claimTask: (task: string, version: number, leaseSeconds = 3600) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/claim`, {
        method: 'POST',
        body: { lease_seconds: leaseSeconds },
        ifMatch: version,
        idempotencyKey: key()
      }),

    renewTask: (task: string, version: number, leaseSeconds = 3600) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/renew`, {
        method: 'POST',
        body: { lease_seconds: leaseSeconds },
        ifMatch: version,
        idempotencyKey: key()
      }),

    releaseTask: (task: string, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/release`, {
        method: 'POST',
        ifMatch: version,
        idempotencyKey: key()
      }),

    completeTask: (task: string, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/complete`, {
        method: 'POST',
        ifMatch: version,
        idempotencyKey: key()
      }),

    blockTask: (task: string, version: number, reason?: string) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/block`, {
        method: 'POST',
        ...(reason === undefined ? {} : { body: { reason } }),
        ifMatch: version,
        idempotencyKey: key()
      }),

    publishAgentWork: (task: string, version: number, input: AgentWorkInput) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/progress`, {
        method: 'POST',
        body: input,
        ifMatch: version,
        idempotencyKey: key()
      }),

    /**
     * Apply one approved audit recommendation through the guarded task move
     * endpoint. The server owns destination positioning and validates the
     * source column, task version, and claim state atomically.
     */
    moveTask: (task: string, input: TaskMoveInput, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/move`, {
        method: 'POST',
        body: input,
        ifMatch: version,
        idempotencyKey: key()
      }),

    reorderTask: (task: string, input: TaskReorderInput, version: number) =>
      request<Task>(`/tasks/${encodeURIComponent(task)}/reorder`, {
        method: 'POST',
        body: input,
        ifMatch: version,
        idempotencyKey: key()
      })
  };
}
