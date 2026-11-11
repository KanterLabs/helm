// Reactive state: The per-project GitHub branch graph.
import type { BranchVisibility } from '../../lib/branchGraph';
import type { GithubBranchReport } from '../../lib/types';

class BranchesState {
  report = $state.raw<GithubBranchReport | null>(null);
  loading = $state.raw(false);
  error = $state.raw('');
  projectId = $state.raw('');
  repository = $state.raw('');
  request = $state.raw(0);
  show = $state.raw<BranchVisibility>({ active: true, stale: false, merged: false });
}

export const branches = new BranchesState();
