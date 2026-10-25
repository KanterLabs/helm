// Offline read-only mode and saved board snapshots.
import { get as readStore } from 'svelte/store';
import { offlineReadOnly } from '../lib/connectivity';
import { clearOfflineBoards, readOfflineBoards, saveOfflineBoard } from '../lib/offlineBoards';
import { bootstrap } from './session';
import { board } from './state/board.svelte';
import { live } from './state/live.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';

export async function enterOffline() {
  const revision = ++board.offlineReadRevision;
  offlineReadOnly.set(true);
  board.boardOffline = true;
  session.sessionGeneration += 1;
  if (live.pollTimer) window.clearInterval(live.pollTimer);
  if (live.pulseTimer) window.clearInterval(live.pulseTimer);
  if (live.livenessRefreshTimer) window.clearInterval(live.livenessRefreshTimer);
  const saved = await readOfflineBoards();
  if (revision === board.offlineReadRevision && readStore(offlineReadOnly)) board.offlineBoards = saved;
  session.booting = false;
}

export async function reconnectOffline() {
  if (board.reconnecting) return;
  board.reconnecting = true;
  board.reconnectError = '';
  try { await bootstrap(); }
  finally {
    board.reconnecting = false;
    if (readStore(offlineReadOnly)) board.reconnectError = session.authError || 'Still offline. Your saved boards remain read-only.';
  }
}

export async function clearSavedBoards() {
  board.offlineReadRevision += 1;
  board.offlineBoards = [];
  await clearOfflineBoards();
  board.offlineBoards = [];
}

export function saveBoardSnapshot() {
  if (readStore(offlineReadOnly) || !session.user || !workspace.activeProject || board.boardLoading || Object.values(board.boardPages).some(page => !page.loaded || page.loading || page.error)) return;
  const filtered = board.boardReleaseFilter !== 'all' || board.boardWorkFilter !== 'all' || Object.entries(board.filters).some(([key, value]) => key === 'query' ? Boolean(value) : value !== 'all');
  void saveOfflineBoard(session.user.id, workspace.activeProject, board.columns, board.tasks, board.boardPartial || filtered);
}
