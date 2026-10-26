// Reactive state: Global search and saved views.
import type { Column, SavedView, Task } from '../../lib/types';
import { taskMatchesReleaseFilter } from '../taskQueries';

class SearchState {
  searchTasks = $state.raw<Task[]>([]);
  searchColumnsByProject = $state.raw<Record<string, Column[]>>({});
  searchSavedViews = $state.raw<SavedView[]>([]);
  searchViewId = $state.raw('');
  searchRouteUsesSavedBaseline = $state.raw(false);
  searchRouteBaselineOverrides = $state.raw<URLSearchParams | null>(null);
  searchQuery = $state.raw('');
  searchPriority = $state.raw('all');
  searchState = $state.raw('all');
  searchSortField = $state.raw('updated_at');
  searchSortDirection = $state.raw<'asc' | 'desc'>('desc');
  searchReleaseFilter = $state.raw('all');
  searchNextCursor = $state.raw('');
  searchLoading = $state.raw(false);
  searchError = $state.raw('');
  searchRequest = $state.raw(0);
  savedViewName = $state.raw('');
  savedViewShared = $state.raw(false);
  savedViewSaving = $state.raw(false);

  // Derived values
  visibleSearchTasks: Task[] = $derived(search.searchTasks.filter((task) => taskMatchesReleaseFilter(task, search.searchReleaseFilter)));
  searchView = $derived(search.searchSavedViews.find((item) => item.id === search.searchViewId));
  searchViewDirty = $derived(Boolean(search.searchView && JSON.stringify([
    search.searchQuery.trim(), search.searchPriority, search.searchState, search.searchReleaseFilter, search.searchSortField, search.searchSortDirection, search.savedViewShared
  ]) !== JSON.stringify([
    search.searchView.filters?.q || search.searchView.filters?.query || '', search.searchView.filters?.priority || 'all', search.searchView.filters?.state || 'all',
    search.searchView.filters?.release_id || 'all', search.searchView.sort?.[0]?.field || 'updated_at',
    search.searchView.sort?.[0]?.direction || 'desc', search.searchView.shared
  ])));
}

export const search = new SearchState();
