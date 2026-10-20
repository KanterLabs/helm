// Display labels, options and timing constants shared by the app shell.
import type { BugResolution, BugSeverity, Priority } from '../lib/types';

export const priorityLabels: Record<Priority, string> = {
  low: 'Low',
  normal: 'Normal',
  high: 'High',
  urgent: 'Urgent'
};

export const stateLabels: Record<string, string> = {
  backlog: 'Backlog',
  ready: 'Ready',
  active: 'In progress',
  blocked: 'Blocked',
  completed: 'Done'
};

export const severityLabels: Record<BugSeverity, string> = {
  s1: 'S1 · Critical',
  s2: 'S2 · High',
  s3: 'S3 · Medium',
  s4: 'S4 · Low'
};

export const resolutionLabels: Record<BugResolution, string> = {
  fixed: 'Fixed',
  duplicate: 'Duplicate',
  not_planned: 'Not planned',
  cannot_reproduce: 'Cannot reproduce',
  works_as_designed: 'Works as designed'
};

export const resolutionOptions: BugResolution[] = ['fixed', 'duplicate', 'not_planned', 'cannot_reproduce', 'works_as_designed'];

export const labelPalette = ['#6d5efc', '#2ea879', '#d49534', '#dc626f', '#4b9cf5'];

export const scopeOptions = ['projects:read', 'projects:write', 'tasks:read', 'tasks:write', 'tasks:claim', 'events:read'];

export const livenessRefreshIntervalMs = 60 * 1000;

export const claimWarningThresholdMs = 5 * 60 * 1000;

export const boardPageSize = 50;

export const boardRenderLimit = 100;
