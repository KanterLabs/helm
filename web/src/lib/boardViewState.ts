import type { BoardFilters } from './state';

export function readBoardView(params: URLSearchParams) {
  const choice = (key: string, values: string[], fallback: string) => {
    const value = params.get(key) || '';
    return values.includes(value) ? value : fallback;
  };
  return {
    filters: {
      query: params.get('q') || '',
      priority: choice('priority', ['urgent', 'high', 'normal', 'low'], 'all'),
      state: choice('state', ['backlog', 'ready', 'active', 'blocked', 'completed'], 'all'),
      label: params.get('label') || 'all', assignee: params.get('assignee') || 'all',
      dependency: choice('dependency', ['blocked', 'ready'], 'all') as BoardFilters['dependency']
    } satisfies BoardFilters,
    work: choice('work', ['action-needed', 'working', 'waiting', 'verifying', 'stale', 'handoff', 'missing'], 'all'),
    release: params.get('release') || 'all',
    sort: choice('sort', ['position', 'number', 'priority', 'title', 'created_at', 'updated_at'], 'position'),
    order: choice('order', ['asc', 'desc'], 'asc') as 'asc' | 'desc'
  };
}

export function boardViewParams(filters: BoardFilters, work: string, release: string, sort: string, order: string): URLSearchParams {
  const params = new URLSearchParams();
  if (filters.query.trim()) params.set('q', filters.query.trim());
  for (const key of ['priority', 'state', 'label', 'assignee', 'dependency'] as const) {
    if (filters[key] && filters[key] !== 'all') params.set(key, filters[key]!);
  }
  if (work !== 'all') params.set('work', work);
  if (release !== 'all') params.set('release', release);
  if (sort !== 'position') params.set('sort', sort);
  if (order !== 'asc') params.set('order', order);
  return params;
}
