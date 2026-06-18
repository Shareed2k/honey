import { apiGet, apiPost, apiDelete } from './core';
import { TunnelInfo } from './types/tunnels';



export async function fetchTunnels(): Promise<TunnelInfo[]> {
  const r = await apiGet('/api/v1/tunnels');
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    throw new Error((j as { error?: string }).error || r.statusText);
  }
  const j = (await r.json()) as { tunnels: TunnelInfo[] };
  return j.tunnels || [];
}

export async function startTunnel(req: { ssh_user: string; record: unknown; mapping: string }): Promise<void> {
  const r = await apiPost('/api/v1/tunnels', req);
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    throw new Error((j as { error?: string }).error || r.statusText);
  }
}

export async function stopTunnel(id: string): Promise<void> {
  const r = await apiDelete(`/api/v1/tunnels/${encodeURIComponent(id)}`);
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    throw new Error((j as { error?: string }).error || r.statusText);
  }
}

export async function fetchTunnelLogs(id: string): Promise<string> {
  const r = await apiGet(`/api/v1/tunnels/${encodeURIComponent(id)}/logs`);
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    throw new Error((j as { error?: string }).error || r.statusText);
  }
  const j = (await r.json()) as { logs: string };
  return j.logs || '';
}