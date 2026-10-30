import type { Collection, Member, MemberCreateInput, MemberPatchInput, SessionDescriptor } from '../types';
import { collectPages, collectionFrom, key, pathWithQuery, request } from './client';

// Human membership administration and local account security. Administrative
// writes carry an Idempotency-Key that callers retain across uncertain
// retries. Password routes reject Idempotency-Key, so they never send one.
// A dropped connection is reported in place (the write may have committed)
// rather than switching the whole workspace into offline read-only mode.
const inPlace = { suppressNetworkUnavailable: true } as const;

export const membershipApi = {
  listMembers: (params: { cursor?: string; limit?: number; disabled?: boolean } = {}, signal?: AbortSignal) =>
    request<Collection<Member>>(pathWithQuery('/members', { cursor: params.cursor, limit: params.limit, disabled: params.disabled }), { signal })
      .then(collectionFrom),

  /** Every page, including pending and (by default) disabled identities. */
  listAllMembers: (params: { disabled?: boolean } = { disabled: true }, signal?: AbortSignal) => collectPages((cursor) =>
    membershipApi.listMembers({ cursor, limit: 100, disabled: params.disabled }, signal)
  ),

  createMember: (input: MemberCreateInput, idempotencyKey = key()) =>
    request<Member>('/members', { method: 'POST', body: input, idempotencyKey, ...inPlace }),

  updateMember: (member: string, input: MemberPatchInput, idempotencyKey = key()) =>
    request<Member>(`/members/${encodeURIComponent(member)}`, { method: 'PATCH', body: input, idempotencyKey, ...inPlace }),

  /** Administrator reset of another member; the server expires every session. */
  resetMemberPassword: (member: string, password: string) =>
    request<{ ok: boolean }>(`/members/${encodeURIComponent(member)}/password`, { method: 'POST', body: { password }, ...inPlace }),

  /** Self change; the server keeps the requesting local session and ends the others. */
  changeOwnPassword: (currentPassword: string, password: string) =>
    request<{ ok: boolean }>('/auth/password', { method: 'POST', body: { current_password: currentPassword, password }, ...inPlace }),

  /** Own sessions, or an administrator's view of an admitted member's sessions. */
  listSessions: (actorId?: string, signal?: AbortSignal) =>
    request<Collection<SessionDescriptor>>(pathWithQuery('/auth/sessions', { actor_id: actorId }), { signal }).then(collectionFrom),

  revokeSession: (session: string, actorId?: string) =>
    request<void>(pathWithQuery(`/auth/sessions/${encodeURIComponent(session)}`, { actor_id: actorId }), { method: 'DELETE', ...inPlace })
};
