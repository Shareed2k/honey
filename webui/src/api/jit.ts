import { apiDelete, apiGet, apiPost, getToken } from './core';

export type JitCapability = 'shell' | 'exec' | 'tunnel';
export type JitDelivery = 'web' | 'cert' | 'both';
export type JitGrantDecision = 'approve' | 'deny' | 'revoke';

export interface JitResourceRef {
  name: string;
  provider: string;
  primary_ip: string;
  meta?: Record<string, string>;
}

export interface CreateGrantRequest {
  resource: JitResourceRef;
  capabilities: JitCapability[];
  delivery: JitDelivery;
  duration: string;
  reason?: string;
  require_approval: boolean;
  max_redemptions: number;
  recipient?: string;
}

export interface CreateGrantResponse {
  id: string;
  code: string;
  link_path: string;
  /** Absolute, reachable share link (server-derived); falls back to link_path + window origin when absent. */
  link?: string;
  status: string;
  expires_at?: string;
  require_approval: boolean;
}

export interface JitGrantView {
  id: string;
  actor: string;
  recipient?: string;
  resource: JitResourceRef;
  capabilities: JitCapability[];
  delivery: JitDelivery;
  reason?: string;
  status: string;
  require_approval: boolean;
  approver?: string;
  created_at?: string;
  decided_at?: string;
  starts_at?: string;
  expires_at?: string;
  max_redemptions: number;
  redemptions: number;
}

/** Shared shape of the meta fields every paginated list endpoint returns alongside its items. */
export interface PageMeta {
  total: number;
  page: number;
  per_page: number;
}

export interface ListGrantsResponse extends PageMeta {
  grants: JitGrantView[];
}

export interface ShareSessionView {
  grant_id: string;
  resource: JitResourceRef;
  actor: string;
  recipient?: string;
  capabilities: JitCapability[];
  created_at: string;
  expires_at?: string;
  redemptions: number;
  max_redemptions: number;
  /** Whether the guest's session is currently live. */
  session_alive: boolean;
  /** How many operators are currently watching this guest session read-only. */
  observers: number;
  /** False when this host has no multiplexer at all, so no guest session redeemed here (past or future) can ever be watched or killed via tmux. */
  observable: boolean;
}

export interface ListShareSessionsResponse extends PageMeta {
  sessions: ShareSessionView[];
}

export interface KillShareSessionResponse {
  grant_id: string;
  /** Whether the guest's session was actually alive (and just terminated) by this call. */
  session_killed: boolean;
}

export interface RedeemStatus {
  status: string;
  active: boolean;
  // Why the link is (in)active: "active" | "pending" | "denied" | "revoked" |
  // "expired" | "exhausted" | "not_started" | "inactive".
  reason?: string;
  resource: { name: string; provider: string };
  capabilities: JitCapability[];
  delivery: JitDelivery;
  expires_at?: string;
  offers: { web: boolean; cert: boolean };
}

export interface RedeemCertResponse {
  cert: string;
  ca: string;
  principals: string[];
  valid_before_unix: number;
}

export async function createGrant(req: CreateGrantRequest): Promise<CreateGrantResponse> {
  const res = await apiPost('/api/v1/jit/grants', req);
  const j = (await res.json().catch(() => ({}))) as CreateGrantResponse & { error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return j;
}

export async function listGrants(page = 1, perPage = 50): Promise<ListGrantsResponse> {
  const res = await apiGet(`/api/v1/jit/grants?page=${page}&per_page=${perPage}`);
  const j = (await res.json().catch(() => ({}))) as Partial<ListGrantsResponse> & { error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return { grants: j.grants ?? [], total: j.total ?? 0, page: j.page ?? page, per_page: j.per_page ?? perPage };
}

export async function decideGrant(id: string, decision: JitGrantDecision): Promise<JitGrantView> {
  const res = await apiPost(`/api/v1/jit/grants/${encodeURIComponent(id)}`, { decision });
  const j = (await res.json().catch(() => ({}))) as JitGrantView & { error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return j;
}

/** Deletes a single TERMINAL grant (denied/revoked/expired). The server refuses (409) an active one. */
export async function deleteGrant(id: string): Promise<void> {
  const res = await apiDelete(`/api/v1/jit/grants/${encodeURIComponent(id)}`);
  if (!res.ok) {
    const j = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || res.statusText);
  }
}

/** Deletes every currently TERMINAL grant ("Delete all finished"). Returns how many were removed. */
export async function purgeGrants(): Promise<number> {
  const res = await apiPost('/api/v1/jit/grants/purge', {});
  const j = (await res.json().catch(() => ({}))) as { deleted?: number; error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return j.deleted ?? 0;
}

/** Lists access-request grants that have, or could have, a guest session, each with its live attachment state. */
export async function listShareSessions(page = 1, perPage = 50): Promise<ListShareSessionsResponse> {
  const res = await apiGet(`/api/v1/share/sessions?page=${page}&per_page=${perPage}`);
  const j = (await res.json().catch(() => ({}))) as Partial<ListShareSessionsResponse> & { error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return { sessions: j.sessions ?? [], total: j.total ?? 0, page: j.page ?? page, per_page: j.per_page ?? perPage };
}

/**
 * Kills an access-request guest session: revokes the grant (the link can
 * never be redeemed again) and terminates the guest's own session — any
 * operator currently watching is disconnected as a side effect. Idempotent:
 * killing an already-revoked or already-ended share is a no-op 200, not an
 * error.
 */
export async function killShareSession(grantId: string): Promise<KillShareSessionResponse> {
  const res = await apiPost(`/api/v1/share/sessions/${encodeURIComponent(grantId)}/kill`, {});
  const j = (await res.json().catch(() => ({}))) as KillShareSessionResponse & { error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return j;
}

/**
 * Builds the authed, operator-only WebSocket URL for the read-only live view
 * of a guest's access-request session (`/ws/share/watch`). Authenticated the
 * same way as the rest of the web UI (the session token), never by a
 * share-link code.
 */
export function shareWatchWebSocketURL(grantId: string): string {
  const token = getToken();
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const u = new URL(`/ws/share/watch?grant=${encodeURIComponent(grantId)}&token=${encodeURIComponent(token)}`, window.location.href);
  u.protocol = proto;
  return u.toString();
}

export async function getRedeemStatus(code: string): Promise<RedeemStatus> {
  const res = await apiGet(`/api/v1/jit/redeem/${encodeURIComponent(code)}`);
  const j = (await res.json().catch(() => ({}))) as RedeemStatus & { error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return j;
}

export async function redeemCert(code: string, publicKey: string): Promise<RedeemCertResponse> {
  const res = await apiPost(`/api/v1/jit/redeem/${encodeURIComponent(code)}/cert`, { public_key: publicKey });
  const j = (await res.json().catch(() => ({}))) as RedeemCertResponse & { error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return j;
}
