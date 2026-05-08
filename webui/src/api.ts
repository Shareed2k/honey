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
export type RecordingListEntry = {
  file_name: string;
  modified_unix_ms: number;
  size_bytes: number;
  trigger?: string;
  mode?: string;
  provider?: string;
  host_name?: string;
  host_ip?: string;
  user?: string;
};

export type RecordingEvent = {
  time_ms: number;
  type: string;
  direction?: string;
  data_b64?: string;
  cols?: number;
  rows?: number;
  message?: string;
  /** Batch exec / CUE: JSON object matching HostExecResultRow when type is "result". */
  result?: HostExecResultRow;
};

export type ConfigSchemaFieldType = 'string' | 'boolean' | 'integer';

export type FileBrowserEntry = {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  mode: string;
  modified_at: string;
};

export type AgentTransferCloud = {
  provider: string;
  bucket: string;
  prefix?: string;
  object?: string;
  region?: string;
  endpoint?: string;
};

export type AgentTransferBackendRef = {
  kind: string;
  name?: string;
  index?: number;
};

export type AgentTransferEvent = {
  stage: string;
  host?: string;
  success: boolean;
  message?: string;
  error?: string;
  attempt?: number;
  timestamp: string;
};

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

export async function listLocalFiles(path: string): Promise<{ root: string; path: string; entries: FileBrowserEntry[] }> {
  const r = await apiPost('/api/v1/files/local/list', { path });
  const j = (await r.json().catch(() => ({}))) as {
    root?: string;
    path?: string;
    entries?: FileBrowserEntry[];
    error?: string;
  };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return { root: j.root || '', path: j.path || '', entries: j.entries || [] };
}

export async function listRemoteFiles(body: {
  ssh_user: string;
  record: unknown;
  path: string;
}): Promise<{ path: string; entries: FileBrowserEntry[] }> {
  const r = await apiPost('/api/v1/files/remote/list', body);
  const j = (await r.json().catch(() => ({}))) as { path?: string; entries?: FileBrowserEntry[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return { path: j.path || '', entries: j.entries || [] };
}

export async function copyFiles(body: {
  direction: 'local_to_remote' | 'remote_to_local';
  ssh_user: string;
  record: unknown;
  local_path: string;
  remote_path: string;
}): Promise<{ status: string; local: string; remote: string }> {
  const r = await apiPost('/api/v1/files/copy', body);
  const j = (await r.json().catch(() => ({}))) as { status?: string; local?: string; remote?: string; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return { status: j.status || 'ok', local: j.local || '', remote: j.remote || '' };
}

export async function startAgentTransfer(body: {
  ssh_user?: string;
  source_record: unknown;
  source_path: string;
  dest_record: unknown;
  dest_path: string;
  cloud: AgentTransferCloud;
  cloud_backend_ref?: AgentTransferBackendRef;
  keep_object?: boolean;
  max_retries?: number;
}): Promise<AgentTransferEvent[]> {
  const r = await apiPost('/api/v1/files/agent-transfer', body);
  const j = (await r.json().catch(() => ({}))) as { events?: AgentTransferEvent[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.events || [];
}

export async function fetchRecipes(): Promise<RecipeListEntry[]> {
  const r = await apiGet('/api/v1/recipes');
  const j = (await r.json().catch(() => ({}))) as { recipes?: RecipeListEntry[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.recipes || [];
}

export async function recipeAssist(body: {
  recipe_path: string;
  model: string;
  user_prompt?: string;
  ssh_user?: string;
  records?: unknown[];
}): Promise<{ reply: string }> {
  const r = await apiPost('/api/v1/recipes/assist', body);
  const j = (await r.json().catch(() => ({}))) as { reply?: string; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return { reply: (j.reply || '').trim() };
}

export async function fetchRecipeContent(path: string): Promise<string> {
  const r = await apiPost('/api/v1/recipes/view', { path });
  const j = (await r.json().catch(() => ({}))) as { content?: string; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.content ?? '';
}

/** List recordings; omit filters to return everything in record-dir (e.g. batch exec files use host_name batch-N). */
export async function fetchRecordingsList(filters?: {
  provider?: string;
  host_name?: string;
  host_ip?: string;
}): Promise<RecordingListEntry[]> {
  const q = new URLSearchParams();
  if (filters?.provider?.trim()) {
    q.set('provider', filters.provider.trim());
  }
  if (filters?.host_name?.trim()) {
    q.set('host_name', filters.host_name.trim());
  }
  if (filters?.host_ip?.trim()) {
    q.set('host_ip', filters.host_ip.trim());
  }
  const qs = q.toString();
  const path = qs ? `/api/v1/recordings?${qs}` : '/api/v1/recordings';
  const r = await apiGet(path);
  const j = (await r.json().catch(() => ({}))) as { items?: RecordingListEntry[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.items || [];
}

export async function fetchRecordingsForHost(params: {
  provider: string;
  host_name: string;
  host_ip: string;
}): Promise<RecordingListEntry[]> {
  return fetchRecordingsList({
    provider: params.provider,
    host_name: params.host_name,
    host_ip: params.host_ip,
  });
}

export async function fetchRecordingEvents(fileName: string): Promise<RecordingEvent[]> {
  const r = await apiPost('/api/v1/recordings/play', { file_name: fileName });
  const j = (await r.json().catch(() => ({}))) as { events?: RecordingEvent[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.events || [];
}

export async function execOnHosts(body: {
  ssh_user: string;
  command: string;
  records: unknown[];
  record_session?: boolean;
}): Promise<HostExecResultRow[]> {
  const r = await apiPost('/api/v1/exec', body);
  const j = (await r.json().catch(() => ({}))) as { results?: HostExecResultRow[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.results || [];
}

async function readNDJSON<T>(
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

export async function execOnHostsStream(
  body: { ssh_user: string; command: string; records: unknown[]; record_session?: boolean },
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
  await readNDJSON<HostExecResultRow>(r, onRow);
}

export async function cueExec(body: {
  recipe_path: string;
  execute: boolean;
  ssh_user: string;
  records: unknown[];
  env?: string[];
  record_session?: boolean;
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
    record_session?: boolean;
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
  await readNDJSON<HostExecResultRow>(r, onRow);
}

export async function startAgentTransferStream(
  body: {
    ssh_user?: string;
    source_record: unknown;
    source_path: string;
    dest_record: unknown;
    dest_path: string;
    cloud: AgentTransferCloud;
    cloud_backend_ref?: AgentTransferBackendRef;
    keep_object?: boolean;
    max_retries?: number;
  },
  onEvent: (event: AgentTransferEvent) => void,
): Promise<void> {
  const r = await fetch('/api/v1/files/agent-transfer?stream=1', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  await readNDJSON<AgentTransferEvent>(r, onEvent);
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
