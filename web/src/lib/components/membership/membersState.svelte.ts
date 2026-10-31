import { api } from '../../api';
import { key as newKey } from '../../api/client';
import { ApiError, type Member, type MemberCreateInput, type MemberPatchInput } from '../../types';

// Private administrator state for the human membership directory. Every
// response is checked against the access generation captured when its
// request started: sign-out, a different signed-in actor, losing admin
// access or unmounting the administration panel bumps the generation and
// clears the state, so a late response can never repopulate it.

class MembersAdminState {
  members = $state.raw<Member[]>([]);
  loading = $state.raw(false);
  loaded = $state.raw(false);
  error = $state.raw('');
}

export const membersAdmin = new MembersAdminState();

let generation = 0;
/** Writes whose outcome is unknown keep their key and exact body until resolved. */
const pendingWrites = new Map<string, { key: string; body: string }>();

export function accessGeneration(): number {
  return generation;
}

export function resetMembershipState(): void {
  generation += 1;
  pendingWrites.clear();
  membersAdmin.members = [];
  membersAdmin.loading = false;
  membersAdmin.loaded = false;
  membersAdmin.error = '';
}

const statusOrder = (member: Member) => (!member.workspace_role ? 0 : member.disabled_at ? 2 : 1);

/** Pending admission first, then active, then disabled; alphabetical within. */
export function sortMembers(members: Member[]): Member[] {
  return [...members].sort((a, b) => statusOrder(a) - statusOrder(b) || a.name.localeCompare(b.name));
}

export function isUncertain(error: unknown): boolean {
  return error instanceof TypeError || (error instanceof ApiError && error.status >= 500);
}

export function describeError(error: unknown, fallback: string): string {
  if (error instanceof TypeError) return `${fallback} Helm could not be reached.`;
  if (!(error instanceof ApiError)) return fallback;
  if (error.code === 'idempotency_key_reused') {
    return 'This change may already have been applied while access changed. Refresh to see the saved state before trying again.';
  }
  if (error.status === 409 && /last active administrator/i.test(error.message)) {
    return 'Helm needs at least one active owner or administrator. Promote another member first.';
  }
  if (error.status === 409 && /exists/i.test(error.message)) return 'A member with that email already exists.';
  if (error.status === 401) return 'Your session has ended. Sign in again to continue.';
  if (error.status === 403) return 'You do not have permission to do that.';
  if (error.status === 404) return 'That member or session no longer exists. Refresh to see the current list.';
  if (error.status === 400 && error.message) return `${error.message.charAt(0).toUpperCase()}${error.message.slice(1)}.`;
  return `${fallback} Please try again.`;
}

async function fetchMembers(): Promise<boolean> {
  const requested = generation;
  membersAdmin.loading = true;
  membersAdmin.error = '';
  try {
    const result = await api.listAllMembers({ disabled: true });
    if (requested !== generation) return false;
    membersAdmin.members = sortMembers(result.data);
    membersAdmin.loaded = true;
    return true;
  } catch (error) {
    if (requested !== generation) return false;
    if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
      membersAdmin.members = [];
      membersAdmin.loaded = false;
    }
    membersAdmin.error = describeError(error, 'Members could not be loaded.');
    return false;
  } finally {
    if (requested === generation) membersAdmin.loading = false;
  }
}

export async function loadMembers(): Promise<void> {
  await fetchMembers();
}

/** Refreshing shows the saved state, which resolves any uncertain write. */
export async function refreshMembers(): Promise<void> {
  // A failed read has not reconciled an unknown write. Keep its original key.
  if (await fetchMembers()) pendingWrites.clear();
}

export type WriteOutcome<T> =
  | { ok: true; value: T }
  | { ok: false; stale: true }
  | { ok: false; stale?: false; uncertain: boolean; message: string };

export function hasUncertainWrite(scope: string): boolean {
  return pendingWrites.has(scope);
}

/**
 * Send an administrative write, reusing the original Idempotency-Key for an
 * identical retry after an uncertain failure. A different change while an
 * earlier one is unresolved is refused: it must not run under a new key
 * until the saved state has been refreshed.
 */
async function retainedWrite<T>(scope: string, body: object, fallback: string, send: (key: string) => Promise<T>): Promise<WriteOutcome<T>> {
  const requested = generation;
  const serialized = JSON.stringify(body);
  const existing = pendingWrites.get(scope);
  if (existing && existing.body !== serialized) {
    return { ok: false, uncertain: true, message: 'An earlier change could not be confirmed. Retry it or refresh members first.' };
  }
  const idempotencyKey = existing?.key ?? newKey();
  pendingWrites.set(scope, { key: idempotencyKey, body: serialized });
  try {
    const value = await send(idempotencyKey);
    if (requested !== generation) return { ok: false, stale: true };
    pendingWrites.delete(scope);
    return { ok: true, value };
  } catch (error) {
    if (requested !== generation) return { ok: false, stale: true };
    const uncertain = isUncertain(error);
    if (!uncertain) pendingWrites.delete(scope);
    const message = uncertain ? "We couldn't confirm whether this change was saved." : describeError(error, fallback);
    return { ok: false, uncertain, message };
  }
}

function replaceMember(member: Member): void {
  const others = membersAdmin.members.filter((item) => item.id !== member.id);
  membersAdmin.members = sortMembers([...others, member]);
}

export async function createMember(input: MemberCreateInput): Promise<WriteOutcome<Member>> {
  const outcome = await retainedWrite('create', input, 'The member could not be created.', (idempotencyKey) => api.createMember(input, idempotencyKey));
  if (outcome.ok) replaceMember(outcome.value);
  return outcome;
}

export async function updateMember(id: string, patch: MemberPatchInput): Promise<WriteOutcome<Member>> {
  const outcome = await retainedWrite(`member:${id}`, patch, 'The member could not be updated.', (idempotencyKey) => api.updateMember(id, patch, idempotencyKey));
  if (outcome.ok) replaceMember(outcome.value);
  return outcome;
}
