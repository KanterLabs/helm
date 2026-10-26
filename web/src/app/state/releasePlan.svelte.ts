// Reactive state: Focus areas (releases), their queues and dialogs.
import { sortReleaseList } from '../releases/load';
import type { ReleaseModel, ReleaseQueueModel } from '../types';
import { workspace } from './workspace.svelte';

class ReleasePlanState {
  releases = $state.raw<ReleaseModel[]>([]);
  releasesByProject = $state.raw<Record<string, ReleaseModel[]>>({});
  releasesLoading = $state.raw(false);
  releasesError = $state.raw('');
  releaseRequest = $state.raw(0);
  releaseRequestsByProject = $state.raw<Record<string, number>>({});
  releaseSelectedId = $state.raw('');
  releaseQueue = $state.raw<ReleaseQueueModel | null>(null);
  releaseQueueLoading = $state.raw(false);
  releaseQueueError = $state.raw('');
  releaseQueueRequest = $state.raw(0);
  showReleaseModal = $state.raw(false);
  releaseEditingId = $state.raw('');
  releaseModalName = $state.raw('');
  releaseModalDescription = $state.raw('');
  releaseModalTargetDate = $state.raw('');
  releaseModalSaving = $state.raw(false);
  releaseModalError = $state.raw('');
  releaseReopenTarget = $state.raw<ReleaseModel | null>(null);
  releaseReopenReason = $state.raw('');
  releaseReopening = $state.raw(false);

  // Derived values
  activeProjectReleases: ReleaseModel[] = $derived(workspace.activeProject ? (releasePlan.releasesByProject[workspace.activeProject.id] || (workspace.activeProject.id === (releasePlan.releases[0]?.project_id || '') ? releasePlan.releases : [])) : []);
  allReleaseOptions: ReleaseModel[] = $derived(Object.values(releasePlan.releasesByProject).flat().reduce<ReleaseModel[]>((result, release) => {
    if (!result.some((item) => item.id === release.id)) result.push(release);
    return result;
  }, []));
  selectedRelease: ReleaseModel | undefined = $derived(releasePlan.releases.find((release) => release.id === releasePlan.releaseSelectedId) || releasePlan.activeProjectReleases.find((release) => release.id === releasePlan.releaseSelectedId));
  sortedReleases: ReleaseModel[] = $derived(sortReleaseList(releasePlan.activeProjectReleases));
}

export const releasePlan = new ReleasePlanState();
