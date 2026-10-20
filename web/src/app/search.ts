// Global search, search URLs and saved views.
import { api } from '../lib/api';
import type { SavedView } from '../lib/types';
import { requestConfirm, restoreDialogFocus } from './dialogs';
import { friendlyError, toast } from './feedback';
import { loadAccessibleReleases } from './releases/load';
import { navigate, setView } from './routing';
import { palette } from './state/palette.svelte';
import { search } from './state/search.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';
import { releaseQueryValue } from './taskQueries';

export function searchSortValue(): string {
  return currentSearchSort().map((term) => `${term.field}:${term.direction}`).join(',');
}

export function currentSearchSort() {
  const saved = search.searchSavedViews.find((item) => item.id === search.searchViewId);
  const first = saved?.sort?.[0];
  if (first?.field === search.searchSortField && first?.direction === search.searchSortDirection) return saved!.sort;
  return [{ field: search.searchSortField, direction: search.searchSortDirection }];
}

export function currentSearchFilters(): SavedView['filters'] {
  const result = { ...(search.searchSavedViews.find((item) => item.id === search.searchViewId)?.filters || {}) };
  for (const key of ['q', 'query', 'priority', 'state', 'release_id']) delete result[key];
  if (search.searchQuery.trim()) result.q = search.searchQuery.trim();
  if (search.searchPriority !== 'all') result.priority = search.searchPriority;
  if (search.searchState !== 'all') result.state = search.searchState;
  if (search.searchReleaseFilter !== 'all') result.release_id = search.searchReleaseFilter;
  return result;
}

export function runSearch() {
  search.searchRouteUsesSavedBaseline = false;
  navigate(searchURL());
  void loadSearch();
}

export function searchURL() {
  const params = new URLSearchParams();
  if (search.searchViewId) {
    params.set('view', search.searchViewId);
    // An explicit criteria marker also represents intentionally empty fields.
    params.set('criteria', '1');
  }
  if (search.searchQuery.trim()) params.set('q', search.searchQuery.trim());
  if (search.searchPriority !== 'all') params.set('priority', search.searchPriority);
  if (search.searchState !== 'all') params.set('state', search.searchState);
  if (search.searchReleaseFilter !== 'all') params.set('release_id', search.searchReleaseFilter);
  params.set('sort', searchSortValue());
  return `/search${params.size ? `?${params}` : ''}`;
}

export function newSearch() {
  if (search.savedViewSaving) return;
  applySearchRouteFilters(new URLSearchParams());
  search.savedViewName = '';
  search.savedViewShared = false;
  navigate('/search');
  void loadSearch();
}

export function applySavedSearch(selected: SavedView) {
  search.searchQuery = typeof selected.filters?.q === 'string' ? selected.filters.q : typeof selected.filters?.query === 'string' ? selected.filters.query : '';
  search.searchPriority = typeof selected.filters?.priority === 'string' ? selected.filters.priority : 'all';
  search.searchState = typeof selected.filters?.state === 'string' ? selected.filters.state : 'all';
  search.searchReleaseFilter = typeof selected.filters?.release_id === 'string' ? selected.filters.release_id : 'all';
  search.searchSortField = selected.sort?.[0]?.field || 'updated_at';
  search.searchSortDirection = selected.sort?.[0]?.direction || 'desc';
  search.savedViewShared = selected.shared;
  search.savedViewName = selected.name;
}

export async function loadSearch(append = false): Promise<boolean> {
  if (append && !search.searchNextCursor) return true;
  const requestId = ++search.searchRequest;
  const requestedSession = session.sessionGeneration;
  const cursor = append ? search.searchNextCursor : undefined;
  if (!append) search.searchNextCursor = '';
  search.searchLoading = true;
  search.searchError = '';
  try {
    if (!search.searchSavedViews.length || search.searchRouteUsesSavedBaseline || search.searchViewId && !search.searchSavedViews.some((item) => item.id === search.searchViewId)) {
      const savedViews = (await api.listAllSavedViews()).data;
      if (requestId !== search.searchRequest || session.sessionGeneration !== requestedSession || workspace.view !== 'search') return false;
      search.searchSavedViews = savedViews;
      const selected = savedViews.find((item) => item.id === search.searchViewId);
      if (search.searchRouteUsesSavedBaseline && selected) {
        applySavedSearch(selected);
        if (search.searchRouteBaselineOverrides) applySearchCriteria(search.searchRouteBaselineOverrides, true);
      } else if (selected) {
        search.savedViewName = selected.name;
        search.savedViewShared = selected.shared;
      }
      if (search.searchViewId && !selected) throw new Error('This saved view is no longer available. Start a new search.');
      search.searchRouteUsesSavedBaseline = false;
      search.searchRouteBaselineOverrides = null;
    }
    const params = {
          view: search.searchViewId || undefined,
          clear: search.searchViewId ? 'q,priority,state,release_id' : undefined,
          q: search.searchQuery.trim() || undefined,
          priority: search.searchPriority === 'all' ? undefined : search.searchPriority,
          state: search.searchState === 'all' ? undefined : search.searchState,
          release_id: releaseQueryValue(search.searchReleaseFilter) || (search.searchReleaseFilter === 'unassigned' ? search.searchReleaseFilter : undefined),
          sort: searchSortValue(),
          cursor,
          limit: 200
        };
    const result = await api.search(params);
    if (requestId !== search.searchRequest || session.sessionGeneration !== requestedSession || !session.user || workspace.view !== 'search') return false;
    search.searchTasks = append ? [...search.searchTasks, ...(result.data || [])] : (result.data || []);
    search.searchNextCursor = result.next_cursor || '';
    const projectIds = Array.from(new Set(search.searchTasks.map((task) => task.project_id)));
    const columnResults = await Promise.all(projectIds.map(async (projectId) => ({
      projectId,
      columns: (await api.listAllColumns(projectId)).data
    })));
    if (requestId !== search.searchRequest || session.sessionGeneration !== requestedSession || !session.user || workspace.view !== 'search') return false;
    search.searchColumnsByProject = Object.fromEntries(columnResults.map((result) => [result.projectId, result.columns]));
    void loadAccessibleReleases();
    return true;
  } catch (error) {
    if (requestId === search.searchRequest && session.sessionGeneration === requestedSession && session.user) {
      search.searchError = friendlyError(error, 'Search could not be loaded.');
    }
    return false;
  } finally {
    if (requestId === search.searchRequest && session.sessionGeneration === requestedSession) search.searchLoading = false;
  }
}

export async function saveCurrentSearchView() {
  if (search.savedViewSaving) return;
  const name = search.savedViewName.trim();
  if (!name) {
    search.searchError = 'Name the saved view before saving it.';
    return;
  }
  search.savedViewSaving = true;
  search.searchError = '';
  const filters: Record<string, unknown> = currentSearchFilters();
  if (search.searchQuery.trim()) filters.q = search.searchQuery.trim();
  if (search.searchPriority !== 'all') filters.priority = search.searchPriority;
  if (search.searchState !== 'all') filters.state = search.searchState;
  if (search.searchReleaseFilter !== 'all') filters.release_id = search.searchReleaseFilter;
  try {
    const created = await api.createSavedView({
      name,
      filters,
      sort: currentSearchSort(),
      shared: search.savedViewShared
    });
    search.searchSavedViews = [created, ...search.searchSavedViews.filter((item) => item.id !== created.id)];
    search.savedViewName = created.name;
    search.searchViewId = created.id;
    navigate(`/search?view=${encodeURIComponent(created.id)}`);
    await loadSearch();
    toast('success', `${created.name} saved.`);
  } catch (error) {
    search.searchError = friendlyError(error, 'The saved view could not be created.');
  } finally {
    search.savedViewSaving = false;
  }
}

export async function updateCurrentSearchView() {
  const current = search.searchSavedViews.find((item) => item.id === search.searchViewId);
  if (!current) return saveCurrentSearchView();
  if (search.savedViewSaving || current.owner_id !== session.user?.id) return;
  search.savedViewSaving = true;
  search.searchError = '';
  try {
    const updated = await api.patchSavedView(current.id, {
      filters: currentSearchFilters(),
      sort: currentSearchSort(),
      shared: search.savedViewShared
    });
    search.searchSavedViews = search.searchSavedViews.map((item) => item.id === updated.id ? updated : item);
    navigate(searchURL(), true);
    toast('success', `${updated.name} updated.`);
    await loadSearch();
  } catch (error) {
    search.searchError = friendlyError(error, 'The saved view could not be updated.');
  } finally {
    search.savedViewSaving = false;
  }
}

export async function renameCurrentSearchView() {
  const current = search.searchSavedViews.find((item) => item.id === search.searchViewId);
  if (!current || search.savedViewSaving || current.owner_id !== session.user?.id || !search.savedViewName.trim()) return;
  search.savedViewSaving = true;
  try {
    const updated = await api.patchSavedView(current.id, { name: search.savedViewName.trim() });
    search.searchSavedViews = search.searchSavedViews.map((item) => item.id === updated.id ? updated : item);
    toast('success', `${updated.name} renamed.`);
  } catch (error) {
    search.searchError = friendlyError(error, 'The saved view could not be renamed.');
  } finally {
    search.savedViewSaving = false;
  }
}

export async function removeCurrentSearchView(savedView: SavedView) {
  const confirmed = await requestConfirm({
    title: `Delete ${savedView.name}?`,
    message: 'This removes the saved search for you. Shared views will also disappear for everyone who can use them.',
    confirmLabel: 'Delete saved view',
    fallbackSelector: '[aria-label="Saved view name"]'
  });
  if (!confirmed) return;
  search.savedViewSaving = true;
  try {
    await api.deleteSavedView(savedView.id);
    search.searchSavedViews = search.searchSavedViews.filter((item) => item.id !== savedView.id);
    if (search.searchViewId === savedView.id) {
      search.searchViewId = '';
      navigate('/search');
      await loadSearch();
    }
    toast('success', `${savedView.name} deleted.`);
  } catch (error) {
    search.searchError = friendlyError(error, 'The saved view could not be deleted.');
  } finally {
    search.savedViewSaving = false;
    restoreDialogFocus();
  }
}

export function applySearchRouteFilters(params: URLSearchParams) {
  search.searchViewId = params.get('view') || '';
  search.searchRouteUsesSavedBaseline = Boolean(search.searchViewId && !params.has('criteria'));
  search.searchRouteBaselineOverrides = search.searchRouteUsesSavedBaseline ? new URLSearchParams(params) : null;
  applySearchCriteria(params);
}

export function applySearchCriteria(params: URLSearchParams, onlyPresent = false) {
  if (!onlyPresent || params.has('q')) search.searchQuery = params.get('q') || '';
  if (!onlyPresent || params.has('priority')) search.searchPriority = params.get('priority') || 'all';
  if (!onlyPresent || params.has('state')) search.searchState = params.get('state') || 'all';
  if (!onlyPresent || params.has('release_id')) search.searchReleaseFilter = params.get('release_id') || 'all';
  if (onlyPresent && !params.has('sort')) return;
  const sort = params.get('sort') || '';
  const [field, direction] = sort.split(',')[0].split(':', 2);
  const validFields = ['updated_at', 'created_at', 'due_at', 'title', 'key', 'priority', 'state', 'position'];
  if (validFields.includes(field) && (direction === 'asc' || direction === 'desc')) {
    search.searchSortField = field;
    search.searchSortDirection = direction;
  } else {
    search.searchSortField = 'updated_at';
    search.searchSortDirection = 'desc';
  }
}

export async function openSavedView(savedView: SavedView) {
  if (search.savedViewSaving) return;
  palette.commandOpen = false;
  search.searchViewId = savedView.id;
  search.searchQuery = typeof savedView.filters?.q === 'string' ? savedView.filters.q : typeof savedView.filters?.query === 'string' ? savedView.filters.query : '';
  search.searchPriority = typeof savedView.filters?.priority === 'string' ? savedView.filters.priority : 'all';
  search.searchState = typeof savedView.filters?.state === 'string' ? savedView.filters.state : 'all';
  search.searchReleaseFilter = typeof savedView.filters?.release_id === 'string' ? savedView.filters.release_id : 'all';
  const firstSort = savedView.sort?.[0];
  search.searchSortField = firstSort?.field || 'updated_at';
  search.searchSortDirection = firstSort?.direction || 'desc';
  search.savedViewName = savedView.name;
  search.savedViewShared = savedView.shared;
  search.searchRouteUsesSavedBaseline = false;
  await setView('search');
}
