import type { BugSeverity, Task } from './tasks';

export type IntakeScope = 'needs_triage' | 'all';

export interface IntakeListParams {
  scope?: IntakeScope;
  kind?: 'task' | 'bug';
  project?: string;
  severity?: BugSeverity | 'untriaged' | 'none';
  q?: string;
  cursor?: string;
  limit?: number;
}

/** Both counts share all field/authorization filters, independent of the page. */
export interface IntakeCollection {
  data: Task[];
  next_cursor: string;
  counts: Record<IntakeScope, number>;
  /** Present only when the existing hidden queue project is readable. */
  queue?: { project_id: string; key: string };
}
