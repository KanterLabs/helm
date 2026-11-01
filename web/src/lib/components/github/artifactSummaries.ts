import { writable } from 'svelte/store';
import { api } from '../../api';
import type { ArtifactSummary } from '../../types';

// Batched board-card PR summaries. Cards register their task; one local
// request per 200 IDs fetches every summary. No card or PR fetches alone.

const BATCH = 200;
const REFRESH_MS = 120_000;

export const artifactSummaries = writable<Record<string, ArtifactSummary>>({});

const tracked = new Map<string, { count: number; version: number }>();
const dirty = new Set<string>();
let flushTimer: ReturnType<typeof setTimeout> | undefined;
let refreshTimer: ReturnType<typeof setTimeout> | undefined;
let inFlight: Promise<void> | null = null;

function schedule(): void {
  if (flushTimer !== undefined) return;
  flushTimer = setTimeout(() => {
    flushTimer = undefined;
    void flush();
  }, 40);
}

function scheduleRefresh(): void {
  if (refreshTimer !== undefined || !tracked.size) return;
  // Jitter keeps many open tabs from reading at the same moment.
  refreshTimer = setTimeout(() => {
    refreshTimer = undefined;
    if (typeof document === 'undefined' || document.visibilityState === 'visible') {
      tracked.forEach((_entry, id) => dirty.add(id));
      schedule();
    }
    scheduleRefresh();
  }, REFRESH_MS + Math.floor(Math.random() * 15_000));
}

async function flush(): Promise<void> {
  if (inFlight) {
    await inFlight;
    if (dirty.size) schedule();
    return;
  }
  const ids = [...dirty].filter((id) => tracked.has(id));
  dirty.clear();
  if (!ids.length) return;
  inFlight = (async () => {
    for (let start = 0; start < ids.length; start += BATCH) {
      const batch = ids.slice(start, start + BATCH);
      try {
        const result = await api.listArtifactSummaries(batch);
        const byId = new Map((result?.data || []).map((item) => [item.task_id, item]));
        artifactSummaries.update((current) => {
          const next = { ...current };
          // A task missing from the response has no linked PRs.
          batch.forEach((id) => {
            const summary = byId.get(id);
            if (summary && summary.count > 0) next[id] = summary;
            else delete next[id];
          });
          return next;
        });
      } catch {
        // Failure shows no badge rather than a stale or invented state;
        // the next version change or refresh cycle retries.
        artifactSummaries.update((current) => {
          const next = { ...current };
          batch.forEach((id) => delete next[id]);
          return next;
        });
      }
    }
  })();
  try {
    await inFlight;
  } finally {
    inFlight = null;
  }
}

/** Register a card. Link/unlink bumps the task version, which re-reads it. */
export function trackArtifactSummary(taskId: string, version: number): () => void {
  const entry = tracked.get(taskId);
  if (!entry) {
    tracked.set(taskId, { count: 1, version });
    dirty.add(taskId);
    schedule();
  } else {
    entry.count += 1;
    if (entry.version !== version) {
      entry.version = version;
      dirty.add(taskId);
      schedule();
    }
  }
  scheduleRefresh();
  return () => {
    const current = tracked.get(taskId);
    if (!current) return;
    current.count -= 1;
    if (current.count <= 0) tracked.delete(taskId);
    if (!tracked.size && refreshTimer !== undefined) {
      clearTimeout(refreshTimer);
      refreshTimer = undefined;
    }
  };
}

export function noteArtifactSummaryVersion(taskId: string, version: number): void {
  const entry = tracked.get(taskId);
  if (!entry || entry.version === version) return;
  entry.version = version;
  dirty.add(taskId);
  schedule();
}

/** The drawer calls this after a link, unlink or refresh changes PR state. */
export function invalidateArtifactSummary(taskId: string): void {
  if (!tracked.has(taskId)) return;
  dirty.add(taskId);
  schedule();
}
