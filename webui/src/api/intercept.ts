import { apiGet, apiPost, getToken } from './core';

export type InterceptOptions = {
  modes: string[];
  udp: boolean;
  command?: string[];
};

export type InterceptSession = {
  id: string;
  record_key?: string;
  name?: string;
  modes?: string[];
  udp?: boolean;
  started_at?: string;
};

/** Builds the `/ws/intercept` URL (ws:// or wss:// to match the current page), same token/auth pattern as `/ws/ssh`. */
export function interceptWebSocketURL(): string {
  const token = getToken();
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const u = new URL(`/ws/intercept?token=${encodeURIComponent(token)}`, window.location.href);
  u.protocol = proto;
  return u.toString();
}

/**
 * Reports whether server-side intercept is enabled. Defensive: `/api/v1/intercept/config`
 * may not exist yet (it is built by a parallel/later backend task), so any error, non-OK
 * response, or missing `enabled` field resolves to `true` — never hard-fail the UI just
 * because this optional endpoint is absent.
 */
export async function fetchInterceptEnabled(): Promise<boolean> {
  try {
    const r = await apiGet('/api/v1/intercept/config');
    if (!r.ok) {
      return true;
    }
    const j = (await r.json().catch(() => ({}))) as { enabled?: boolean };
    return j.enabled !== false;
  } catch {
    return true;
  }
}

/**
 * Lists active intercept sessions. Defensive: `/api/v1/intercept/sessions` is added by a
 * later backend task, so an absent endpoint or any error resolves to an empty list rather
 * than throwing — callers should hide the active-intercepts panel when this is empty.
 */
export async function fetchInterceptSessions(): Promise<InterceptSession[]> {
  try {
    const r = await apiGet('/api/v1/intercept/sessions');
    if (!r.ok) {
      return [];
    }
    const j = (await r.json().catch(() => ({}))) as { sessions?: InterceptSession[] };
    return Array.isArray(j.sessions) ? j.sessions : [];
  } catch {
    return [];
  }
}

/** Stops an active intercept session. Throws on failure (unlike the list/config helpers) so callers can surface the error. */
export async function stopInterceptSession(id: string): Promise<void> {
  const r = await apiPost(`/api/v1/intercept/${encodeURIComponent(id)}/stop`, {});
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    throw new Error((j as { error?: string }).error || r.statusText);
  }
}
