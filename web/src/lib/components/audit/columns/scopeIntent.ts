// The person's intended audit scope survives a settings detour, reload or
// route change in this tab. It holds IDs and choices only, never task content.

export type ScopeMode = 'all' | 'columns' | 'selected';

export interface ScopeIntent {
  mode: ScopeMode;
  columnIds: string[];
  taskIds: string[];
  /** Per selected repository: the saved ref, or an explicit PR head number. */
  baselines: Record<string, { kind: 'saved' | 'pr'; pullRequest: string }>;
  /** Set by an entry point to open the scope panel once. */
  open?: boolean;
}

const storageKey = (projectId: string) => `helm.columnAudit.scope.${projectId}`;

export const defaultIntent = (): ScopeIntent => ({ mode: 'all', columnIds: [], taskIds: [], baselines: {} });

function storage(): Storage | null {
  try {
    return typeof sessionStorage === 'undefined' ? null : sessionStorage;
  } catch {
    return null;
  }
}

export function readIntent(projectId: string): ScopeIntent {
  const raw = storage()?.getItem(storageKey(projectId));
  if (!raw) return defaultIntent();
  try {
    const parsed = JSON.parse(raw) as Partial<ScopeIntent>;
    const ids = (value: unknown) => (Array.isArray(value) ? value.filter((id): id is string => typeof id === 'string').slice(0, 1000) : []);
    return {
      mode: parsed.mode === 'columns' || parsed.mode === 'selected' ? parsed.mode : 'all',
      columnIds: ids(parsed.columnIds),
      taskIds: ids(parsed.taskIds),
      baselines: parsed.baselines && typeof parsed.baselines === 'object' ? parsed.baselines : {},
      open: parsed.open === true
    };
  } catch {
    return defaultIntent();
  }
}

export function writeIntent(projectId: string, intent: ScopeIntent): void {
  storage()?.setItem(storageKey(projectId), JSON.stringify(intent));
}

/** Entry points: board Actions, selected cards and Re-audit. */
export function requestScope(projectId: string, taskIds: string[] = []): void {
  const current = readIntent(projectId);
  writeIntent(projectId, taskIds.length
    ? { ...current, mode: 'selected', taskIds: taskIds.slice(0, 1000), open: true }
    : { ...current, mode: current.mode === 'selected' ? 'all' : current.mode, open: true });
}

/** Read and clear the one-shot open request. */
export function consumeOpenRequest(projectId: string): boolean {
  const intent = readIntent(projectId);
  if (!intent.open) return false;
  writeIntent(projectId, { ...intent, open: false });
  return true;
}
