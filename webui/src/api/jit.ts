import { apiGet, apiPost } from './core';

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

export async function listGrants(): Promise<JitGrantView[]> {
  const res = await apiGet('/api/v1/jit/grants');
  const j = (await res.json().catch(() => ({}))) as { grants?: JitGrantView[]; error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return j.grants ?? [];
}

export async function decideGrant(id: string, decision: JitGrantDecision): Promise<JitGrantView> {
  const res = await apiPost(`/api/v1/jit/grants/${encodeURIComponent(id)}`, { decision });
  const j = (await res.json().catch(() => ({}))) as JitGrantView & { error?: string };
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
