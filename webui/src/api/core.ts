import { RecentRunEntry } from './types/recipes';
import { AppConfig } from './types/proxy';



export const TOKEN_KEY = 'honey_web_token';

export function getToken(): string {
  const q = new URLSearchParams(window.location.search).get('token');
  if (q) {
    sessionStorage.setItem(TOKEN_KEY, q);
    return q;
  }
  return sessionStorage.getItem(TOKEN_KEY) || '';
}

export function apiHeaders(): Record<string, string> {
  const t = getToken();
  const h: Record<string, string> = { Accept: 'application/json' };
  if (t) {
    h.Authorization = `Bearer ${t}`;
    h['X-Honey-Token'] = t;
  }
  return h;
}

export async function apiGet(path: string): Promise<Response> {
  return fetch(path, { headers: apiHeaders() });
}

export async function apiPost(path: string, body: unknown): Promise<Response> {
  return fetch(path, {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

export async function apiPut(path: string, body: BodyInit, contentType?: string): Promise<Response> {
  const h: Record<string, string> = { ...apiHeaders() } as Record<string, string>;
  if (contentType) {
    h['Content-Type'] = contentType;
  }
  return fetch(path, { method: 'PUT', headers: h, body });
}

export async function apiPutJson(path: string, body: unknown): Promise<Response> {
  return fetch(path, {
    method: 'PUT',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

export async function apiDelete(path: string): Promise<Response> {
  return fetch(path, { method: 'DELETE', headers: apiHeaders() });
}

export async function fetchRecentRuns(limit = 20): Promise<RecentRunEntry[]> {
  const r = await apiGet(`/api/v1/recipes/recent-runs?limit=${encodeURIComponent(limit)}`);
  const j = (await r.json().catch(() => ({}))) as { runs?: RecentRunEntry[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.runs ?? [];
}

export async function fetchApps(): Promise<{ [key: string]: AppConfig }> {
  const res = await apiGet('/api/v1/apps');
  if (!res.ok) {
    throw new Error(res.statusText);
  }
  const data = await res.json();
  return data.apps || {};
}

export async function readNDJSON<T>(
  response: Response,
  onRow: (row: T) => void,
): Promise<void> {
  const reader = response.body?.getReader();
  if (!reader) {
    throw new Error('stream missing response body');
  }
  const decoder = new TextDecoder();
  let buffer = '';
  while (true) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() || '';
    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed) {
        continue;
      }
      onRow(JSON.parse(trimmed) as T);
    }
  }
  const tail = buffer.trim();
  if (tail) {
    onRow(JSON.parse(tail) as T);
  }
}