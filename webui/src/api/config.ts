import { apiGet } from './core';
import { ConfigSchemaResponse } from './types/config';



export async function fetchConfigSchema(): Promise<ConfigSchemaResponse> {
  const r = await apiGet('/api/v1/config/schema');
  const j = (await r.json().catch(() => ({}))) as ConfigSchemaResponse & { error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j;
}