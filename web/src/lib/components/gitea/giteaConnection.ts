import { ApiError } from '../../types';

// Pure helpers for the Gitea connection panel: product naming, messages,
// client ID extraction, the sign-in address guard and the saved draft.
// Standard: docs/GITEA_OAUTH_UX.md. Failure list: ./failures.md.

export type ProductLabel = 'Gitea' | 'Forgejo';

export const productLabel = (product: string | null | undefined): ProductLabel => (product === 'forgejo' ? 'Forgejo' : 'Gitea');

/** `https://` is implied; `http://` stays visible because it matters. */
export function displayAddress(base: string): string {
  return base.replace(/^https:\/\//i, '').replace(/\/+$/, '');
}

/** True once a typed value plausibly names a host, so a check may start. */
export function looksLikeHost(value: string): boolean {
  const text = value.trim();
  if (text.length < 4 || /\s/.test(text)) return false;
  const host = text.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '').split(/[/?#]/)[0].replace(/:\d{1,5}$/, '');
  return /^localhost$/i.test(host) || /^\d{1,3}(\.\d{1,3}){3}$/.test(host) || /^([a-z0-9-]+\.)+[a-z]{2,}$/i.test(host);
}

export const CLIENT_ID_PATTERN = /^[A-Za-z0-9._-]{1,128}$/;
const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

/**
 * Finds the client ID in pasted text: a bare ID, a UUID anywhere (Gitea
 * issues UUIDs), or the token after a "Client ID" label. Falls back to the
 * trimmed text so the person sees what they pasted.
 */
export function extractClientId(text: string): string {
  const trimmed = text.trim();
  if (CLIENT_ID_PATTERN.test(trimmed)) return trimmed;
  const uuid = trimmed.match(UUID);
  if (uuid) return uuid[0];
  const labelled = trimmed.match(/client[\s_-]*id\s*[:=]?\s*([A-Za-z0-9._-]{1,128})/i);
  if (labelled) return labelled[1];
  return trimmed;
}

/**
 * The browser goes only to the probed forge: same scheme and host, and a
 * path under the probed base path. A bare prefix test would let
 * `https://forge.example.com.evil.example/` through.
 */
export function authorizeAllowed(target: string, base: string): boolean {
  try {
    const url = new URL(target);
    const root = new URL(base.endsWith('/') ? base : `${base}/`);
    return url.protocol === root.protocol && url.host === root.host && url.username === '' && url.password === '' && url.pathname.startsWith(root.pathname);
  } catch {
    return false;
  }
}

export type MessageAction =
  | 'none' | 'focus-address' | 'retry' | 'guide' | 'use-issuer' | 'show-connection' | 'sign-in'
  | 'revoke-link' | 'focus-client' | 'register-link' | 'http-consent';

export type Message = { code: string; text: string; action: MessageAction; issuer?: string };

/** The message and action for a code. Never shows upstream text or a raw code. */
export function messageFor(code: string, product: ProductLabel, details: Record<string, unknown> = {}, reason = ''): Message {
  const p = product;
  const make = (text: string, action: MessageAction): Message => ({ code, text, action });
  switch (code) {
    case 'invalid_server_url': return make('That does not look like a server address. Try gitea.example.com.', 'focus-address');
    case 'insecure_http': return make('This address uses http, so sign-in details could be read on the network.', 'http-consent');
    case 'server_unreachable': return make('Helm could not reach that server. Check the address, and that the Helm server can reach it on your network.', 'retry');
    case 'tls_error': return make("Helm does not trust that server's certificate. If it uses a private certificate authority, see the setup guide.", 'guide');
    case 'not_a_forge': return make(`That server did not answer like Gitea or Forgejo. If ${p} lives under a path, include it.`, 'focus-address');
    case 'issuer_mismatch': {
      const issuer = typeof details.issuer === 'string' ? details.issuer : '';
      if (!issuer) return make('Something went wrong connecting to ' + p + '.', 'retry');
      return { code, text: `This server says its address is ${issuer}.`, action: 'use-issuer', issuer };
    }
    case 'already_connected': return make('Helm is already connected. Disconnect first.', 'show-connection');
    case 'state_invalid': return make('That sign-in expired or was started somewhere else.', 'sign-in');
    case 'denied': return make(`Sign-in was cancelled on ${p}. Nothing was changed.`, 'sign-in');
    case 'scope_conflict': return make(`${p} still holds an older authorization for Helm. Remove Helm under Authorized OAuth2 Applications, then sign in again.`, 'revoke-link');
    case 'authorize_failed': return make(`${p} refused the sign-in. Check the Client ID.`, 'focus-client');
    case 'client_confidential': return make(`The application is set as a Confidential Client. Edit it in ${p}, untick Confidential Client, save, then sign in again.`, 'register-link');
    case 'client_unknown': return make(`${p} does not recognise that Client ID. Copy it again from the application page.`, 'focus-client');
    case 'exchange_failed': return make(`${p} did not complete the sign-in.`, 'sign-in');
    case 'storage_failed': return make('Helm could not save the connection. Check that its data directory is writable.', 'none');
    case 'reconnect_required': return make(`Helm's sign-in to ${p} has ended.`, 'sign-in');
    case 'unavailable': return make(unavailableText(reason, p), 'none');
    case 'authorize_outside': return make(`Helm did not open the sign-in page because it was not on the server you checked. Try again; if this repeats, check the server address.`, 'retry');
    default: return make(`Something went wrong connecting to ${p}.`, 'retry');
  }
}

export function unavailableText(reason: string, product: ProductLabel): string {
  return reason === 'public_origin_missing'
    ? "Sign-in needs Helm's public address. An operator sets HELM_PUBLIC_ORIGIN."
    : `${product} sign-in is turned off on this server.`;
}

/** The C1 code of a failed request; network failures count as "other". */
export function errorCode(failure: unknown): { code: string; details: Record<string, unknown> } {
  if (failure instanceof ApiError) return { code: failure.code, details: (failure.details as Record<string, unknown>) || {} };
  return { code: 'request_failed', details: {} };
}

// The address and client ID are not secrets; keeping them for the tab lets
// a failed sign-in return to step 2 intact. Cleared on success and disconnect.
export const DRAFT_KEY = 'helm.gitea.draft';
export type Draft = { base_url: string; client_id: string; allow_insecure_http: boolean };

export function loadDraft(): Draft | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(DRAFT_KEY) || 'null') as Partial<Draft> | null;
    if (!value || typeof value.base_url !== 'string' || !value.base_url) return null;
    return { base_url: value.base_url.slice(0, 255), client_id: typeof value.client_id === 'string' ? value.client_id.slice(0, 128) : '', allow_insecure_http: value.allow_insecure_http === true };
  } catch {
    return null;
  }
}

export function saveDraft(draft: Draft): void {
  try {
    sessionStorage.setItem(DRAFT_KEY, JSON.stringify(draft));
  } catch {
    // Storage may be unavailable (private mode); only the convenience is lost.
  }
}

export function clearDraft(): void {
  try {
    sessionStorage.removeItem(DRAFT_KEY);
  } catch {
    // As above.
  }
}

/** Reads and removes `gitea` / `gitea_error` from the address bar. */
export function takeReturnParams(): { connected: boolean; error: string } {
  const url = new URL(window.location.href);
  const connected = url.searchParams.get('gitea') === 'connected';
  const error = (url.searchParams.get('gitea_error') || '').slice(0, 64);
  if (url.searchParams.has('gitea') || url.searchParams.has('gitea_error')) {
    url.searchParams.delete('gitea');
    url.searchParams.delete('gitea_error');
    window.history.replaceState(window.history.state, '', url.pathname + url.search + url.hash);
  }
  return { connected, error };
}
