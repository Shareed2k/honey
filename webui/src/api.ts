const TOKEN_KEY = 'honey_web_token';

export function getToken(): string {
  const q = new URLSearchParams(window.location.search).get('token');
  if (q) {
    sessionStorage.setItem(TOKEN_KEY, q);
    return q;
  }
  return sessionStorage.getItem(TOKEN_KEY) || '';
}

export function apiHeaders(): HeadersInit {
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

/** Matches Go ui.HostExecResult JSON (exported struct fields). */
export type HostExecResultRow = {
  Name: string;
  IP: string;
  Provider: string;
  Success: boolean;
  ExitCode: number;
  Output: string;
  ErrMsg: string;
};

export type RecipeListEntry = { name: string; path: string };

export type ConfigSchemaFieldType = 'string' | 'boolean' | 'integer';

export type ConfigSchemaFieldSpec = {
  key: string;
  label: string;
  type: ConfigSchemaFieldType;
  required?: boolean;
  secret?: boolean;
  enum?: string[];
  enum_as_warning?: boolean;
  default?: unknown;
};

export type ConfigBackendSchema = {
  label: string;
  fields: ConfigSchemaFieldSpec[];
};

export type ConfigUISchema = {
  top_level_keys: string[];
  defaults: ConfigSchemaFieldSpec[];
  backends: Record<string, ConfigBackendSchema>;
  backend_order: string[];
};

export type ConfigSchemaResponse = {
  json_schema: Record<string, unknown>;
  ui_schema: ConfigUISchema;
};

export async function fetchConfigSchema(): Promise<ConfigSchemaResponse> {
  const r = await apiGet('/api/v1/config/schema');
  const j = (await r.json().catch(() => ({}))) as ConfigSchemaResponse & { error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j;
}

export async function fetchRecipes(): Promise<RecipeListEntry[]> {
  const r = await apiGet('/api/v1/recipes');
  const j = (await r.json().catch(() => ({}))) as { recipes?: RecipeListEntry[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.recipes || [];
}

export async function fetchRecipeContent(path: string): Promise<string> {
  const r = await apiPost('/api/v1/recipes/view', { path });
  const j = (await r.json().catch(() => ({}))) as { content?: string; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.content ?? '';
}

export async function execOnHosts(body: {
  ssh_user: string;
  command: string;
  records: unknown[];
}): Promise<HostExecResultRow[]> {
  const r = await apiPost('/api/v1/exec', body);
  const j = (await r.json().catch(() => ({}))) as { results?: HostExecResultRow[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.results || [];
}

async function readNDJSON(
  response: Response,
  onRow: (row: HostExecResultRow) => void,
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
      onRow(JSON.parse(trimmed) as HostExecResultRow);
    }
  }
  const tail = buffer.trim();
  if (tail) {
    onRow(JSON.parse(tail) as HostExecResultRow);
  }
}

export async function execOnHostsStream(
  body: { ssh_user: string; command: string; records: unknown[] },
  onRow: (row: HostExecResultRow) => void,
): Promise<void> {
  const r = await fetch('/api/v1/exec?stream=1', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  await readNDJSON(r, onRow);
}

export async function cueExec(body: {
  recipe_path: string;
  execute: boolean;
  ssh_user: string;
  records: unknown[];
  env?: string[];
}): Promise<{ plan?: string; results?: HostExecResultRow[] }> {
  const r = await apiPost('/api/v1/cue-exec', body);
  const j = (await r.json().catch(() => ({}))) as {
    plan?: string;
    results?: HostExecResultRow[];
    error?: string;
  };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return { plan: j.plan, results: j.results };
}

export async function cueExecStream(
  body: {
    recipe_path: string;
    execute: boolean;
    ssh_user: string;
    records: unknown[];
    env?: string[];
  },
  onRow: (row: HostExecResultRow) => void,
): Promise<void> {
  const r = await fetch('/api/v1/cue-exec?stream=1', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  await readNDJSON(r, onRow);
}

/** POST multipart FormData with upload progress (bytes to this origin only). Resolves parsed JSON body. */
export function uploadFormDataWithProgress(
  url: string,
  formData: FormData,
  onProgress: (percent: number) => void,
): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', url);
    const h = apiHeaders() as Record<string, string>;
    for (const [k, v] of Object.entries(h)) {
      xhr.setRequestHeader(k, v);
    }
    xhr.upload.onprogress = (ev) => {
      if (ev.lengthComputable && ev.total > 0) {
        onProgress(Math.round((100 * ev.loaded) / ev.total));
      }
    };
    xhr.onload = () => {
      let body: unknown;
      try {
        body = JSON.parse(xhr.responseText) as unknown;
      } catch {
        body = xhr.responseText;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(body);
      } else {
        let msg = xhr.statusText || `HTTP ${xhr.status}`;
        if (typeof body === 'object' && body !== null && 'error' in body) {
          msg = String((body as { error: string }).error);
        } else if (typeof body === 'string' && body.trim()) {
          msg = body.trim().slice(0, 800);
        }
        reject(new Error(msg));
      }
    };
    xhr.onerror = () => reject(new Error('network error'));
    xhr.send(formData);
  });
}
