import { apiGet, apiPost } from './core';
import { type HostExecResultRow } from './types/exec';

export interface WebhookDebugResponse {
  auth_ok: boolean;
  extracted: { [k: string]: string };
  actor: string;
  idempotency_key?: string;
  async: boolean;
  executed: boolean;
  outcome: string;
  exec_id?: string;
  results?: HostExecResultRow[];
  error?: string;
}

export interface WebhookDelivery {
  id: string;
  source: string; // live | test | dry_run
  received_at: string;
  remote_addr?: string;
  content_type?: string;
  body: string;
  auth_ok: boolean;
  extracted?: { [k: string]: string };
  actor?: string;
  idempotency_key?: string;
  async: boolean;
  outcome: string;
  exec_id?: string;
  error?: string;
  results?: HostExecResultRow[];
}

// debugWebhook previews (execute=false) or runs (execute=true) a webhook against
// a test payload. The payload is the raw webhook body.
export async function debugWebhook(
  app: string,
  webhook: string,
  payload: unknown,
  execute: boolean,
): Promise<WebhookDebugResponse> {
  const r = await apiPost(
    `/api/v1/webhooks/${encodeURIComponent(app)}/${encodeURIComponent(webhook)}/debug`,
    { payload, execute },
  );
  const j = (await r.json().catch(() => ({}))) as WebhookDebugResponse & { error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j;
}

export interface WebhookResult {
  id: string;
  status: string;
  started_at?: string;
  results?: HostExecResultRow[];
}

// getWebhookResult fetches an async webhook execution's recorded results.
// Returns null while the recording is not yet available (404).
export async function getWebhookResult(id: string): Promise<WebhookResult | null> {
  const r = await apiGet(`/api/v1/webhooks/results/${encodeURIComponent(id)}`);
  if (r.status === 404) {
    return null; // not recorded yet — keep polling
  }
  const j = (await r.json().catch(() => ({}))) as WebhookResult & { error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j;
}

export async function listWebhookDeliveries(
  app: string,
  webhook: string,
  limit = 20,
): Promise<WebhookDelivery[]> {
  const r = await apiGet(
    `/api/v1/webhooks/${encodeURIComponent(app)}/${encodeURIComponent(webhook)}/deliveries?limit=${encodeURIComponent(limit)}`,
  );
  const j = (await r.json().catch(() => ({}))) as { deliveries?: WebhookDelivery[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.deliveries ?? [];
}
