import { apiDelete, apiGet, apiPost } from './core';

export type JitCapability = 'shell' | 'exec' | 'tunnel' | 'watch' | 'collaborate';
export type JitDelivery = 'web' | 'cert' | 'both';
export type JitGrantDecision = 'approve' | 'deny' | 'revoke';

/** watch = read-only attach, collaborate = read-write attach to an existing live terminal. */
export type LiveTerminalCapability = 'watch' | 'collaborate';

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
  /**
   * live_terminal share extension: set kind to attach the redeemer to an
   * operator's EXISTING tmux-backed session instead of a brand-new shell.
   * mux_session/capability replace `capabilities` (which is ignored server-side
   * when kind is set); delivery is forced to "web" server-side regardless of
   * what is sent.
   */
  kind?: 'live_terminal';
  mux_session?: string;
  capability?: LiveTerminalCapability;
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
  capability: LiveTerminalCapability;
  mux_session: string;
  actor: string;
  created_at: string;
  expires_at?: string;
  redemptions: number;
  max_redemptions: number;
  /** How many read-only (guest) tmux clients are attached right now — the whole point of this panel. */
  attached_guests: number;
  /** Whether the underlying tmux session is still alive (false when tmux/the session is gone). */
  session_alive: boolean;
}

export interface ListShareSessionsResponse extends PageMeta {
  sessions: ShareSessionView[];
}

export interface KillShareSessionResponse {
  grant_id: string;
  /** How many guest clients were detached by this call. */
  detached: number;
  /** How many guest clients remain attached afterward (normally 0). */
  attached_guests: number;
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

/** Lists the live-terminal shares that are currently redeemable/active, each with its live attachment state. */
export async function listShareSessions(page = 1, perPage = 50): Promise<ListShareSessionsResponse> {
  const res = await apiGet(`/api/v1/share/sessions?page=${page}&per_page=${perPage}`);
  const j = (await res.json().catch(() => ({}))) as Partial<ListShareSessionsResponse> & { error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return { sessions: j.sessions ?? [], total: j.total ?? 0, page: j.page ?? page, per_page: j.per_page ?? perPage };
}

/**
 * Kills a live-terminal share: revokes the grant (the link can never be
 * redeemed again) and disconnects every guest currently attached — the
 * operator's own terminal session is never touched. Idempotent: killing an
 * already-revoked or already-ended share is a no-op 200, not an error.
 */
export async function killShareSession(grantId: string): Promise<KillShareSessionResponse> {
  const res = await apiPost(`/api/v1/share/sessions/${encodeURIComponent(grantId)}/kill`, {});
  const j = (await res.json().catch(() => ({}))) as KillShareSessionResponse & { error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return j;
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
