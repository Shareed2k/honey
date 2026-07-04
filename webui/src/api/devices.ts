import { apiGet, apiPost } from './core';

export interface MintEnrollCodeResponse {
  code: string;
  cn: string;
  enroll_path: string;
  ca_fingerprint: string;
  ca_pem: string;
  expires_in: number;
}

export interface DeviceRecord {
  cn: string;
  fingerprint: string;
  issued_at: string;
  not_after: string;
}

// The bootstrap payload encoded into the QR the app scans.
export interface EnrollBootstrap {
  enroll_url: string;
  code: string;
  ca_fingerprint: string;
  cn: string;
}

export async function mintEnrollCode(cn?: string): Promise<MintEnrollCodeResponse> {
  const res = await apiPost('/api/v1/devices/enroll-code', { cn: cn ?? '' });
  const j = (await res.json().catch(() => ({}))) as MintEnrollCodeResponse & { error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return j;
}

export async function listDevices(): Promise<DeviceRecord[]> {
  const res = await apiGet('/api/v1/devices');
  const j = (await res.json().catch(() => ({}))) as { devices?: DeviceRecord[]; error?: string };
  if (!res.ok) {
    throw new Error(j.error || res.statusText);
  }
  return j.devices ?? [];
}

// bootstrapFor builds the QR payload from a mint response. The enroll URL is
// derived from the given base (default: the current origin) + the enroll path.
export function bootstrapFor(m: MintEnrollCodeResponse, base?: string): EnrollBootstrap {
  const origin = (base && base.trim()) || window.location.origin;
  return {
    enroll_url: origin.replace(/\/$/, '') + m.enroll_path,
    code: m.code,
    ca_fingerprint: m.ca_fingerprint,
    cn: m.cn,
  };
}
