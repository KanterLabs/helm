import type { Project } from './core';
import type { ReleaseFilterValue } from './releases';
import type { Task } from './tasks';

/** Stable ordering terms accepted by global search and saved views. */
export interface SearchSort {
  field: 'updated_at' | 'created_at' | 'due_at' | 'title' | 'key' | 'priority' | 'state' | 'position' | string;
  direction: 'asc' | 'desc';
}

/** Persisted search/view filters. `release_id` is always an opaque ID or the
 * `unassigned` sentinel; global views never persist a release name. */
export interface SavedViewFilters extends Record<string, unknown> {
  release_id?: ReleaseFilterValue;
}

export interface SavedView {
  id: string;
  owner_id: string;
  name: string;
  description?: string;
  filters: SavedViewFilters;
  sort: SearchSort[];
  shared: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface SearchResponse {
  data: Task[];
  next_cursor?: string | null;
  projects?: Project[];
  views?: SavedView[];
}
