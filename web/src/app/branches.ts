// The per-project GitHub branch graph: loading, repository choice and filters.
import { api } from '../lib/api';
import type { BranchBucket } from '../lib/branchGraph';
import { friendlyError } from './feedback';
import { setView } from './routing';
import { branches } from './state/branches.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';

/** Drops branch names of the previous project or session before any await. */
export function resetBranches() {
  branches.request += 1;
  branches.report = null;
  branches.loading = false;
  branches.error = '';
  branches.projectId = '';
  branches.repository = '';
}

export async function loadBranches(projectId = workspace.activeProject?.id, repository = branches.repository): Promise<void> {
  if (!projectId) return;
  if (branches.projectId !== projectId) {
    resetBranches();
    repository = '';
  }
  const requestId = ++branches.request;
  const requestedSession = session.sessionGeneration;
  const superseded = () => requestId !== branches.request
    || requestedSession !== session.sessionGeneration
    || !session.user
    || workspace.view !== 'branches'
    || workspace.activeProject?.id !== projectId;
  branches.projectId = projectId;
  branches.loading = true;
  branches.error = '';
  try {
    const report = await api.getProjectGithubBranches(projectId, repository || undefined);
    if (superseded()) return;
    branches.report = report;
    branches.repository = report.repository;
  } catch (error) {
    if (superseded()) return;
    // A failed refresh must not leave earlier private branch names on screen.
    branches.report = null;
    branches.error = friendlyError(error, 'Branches could not be loaded.');
  } finally {
    if (requestId === branches.request) branches.loading = false;
  }
}

export async function openProjectBranches() {
  if (workspace.activeProject) await setView('branches');
}

export function selectBranchRepository(repository: string) {
  if (repository === branches.repository) return;
  branches.report = null;
  void loadBranches(workspace.activeProject?.id, repository);
}

export function toggleBranchBucket(bucket: BranchBucket) {
  branches.show = { ...branches.show, [bucket]: !branches.show[bucket] };
}
