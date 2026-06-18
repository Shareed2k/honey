import { apiPost } from './core';



export async function encryptSecret(plaintext: string): Promise<string> {
  const res = await apiPost('/api/v1/secrets/encrypt', { plaintext });
  if (!res.ok) {
    const j = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || res.statusText);
  }
  const data = await res.json();
  return data.encrypted;
}