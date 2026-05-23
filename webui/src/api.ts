import type { HostRecord } from './HostPicker';

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

export type TunnelInfo = {
  id: string;
  host: string;
  record_key: string;
  mapping: string;
  started_at: string;
  error?: string;
};

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

export async function fetchHostPorts(req: { ssh_user: string; record: unknown }): Promise<string[]> {
  const r = await apiPost('/api/v1/host-ports', req);
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    throw new Error((j as { error?: string }).error || r.statusText);
  }
  const j = (await r.json()) as { ports: string[] };
  return j.ports || [];
}

/** Matches Go ui.HostExecResult JSON (exported struct fields). */
export type HostExecResultRow = {
  Name: string;
  IP: string;
  Provider: string;
  Success: boolean;
  Skipped?: boolean;
  ExitCode: number;
  Output: string;
  ErrMsg: string;
  HookPhase?: string;
  HookOutput?: string;
};

export type RecipeListEntry = { name: string; path: string };

/** Structured recipe shape that mirrors internal/cuetry.Recipe (JSON keys match Go json tags). */
export type ParsedRecipe = {
  name: string;
  type?: string;
  defaults?: Record<string, unknown>;
  steps: ParsedRecipeStep[];
};

export type ParsedRecipeEnvFrom = {
  step?: string;
  from_output?: string;
  map: Record<string, string>;
};

export type ParsedRecipeStepTemplate = {
  template: string;
  data?: Record<string, unknown>;
  output?: string;
};

export type ParsedRecipeStepRetry = {
  attempts?: number;
  delay_ms?: number;
  max_delay_ms?: number;
  backoff?: 'fixed' | 'exponential';
};

export type ParsedRecipeFileTransfer = {
  local: string;
  remote: string;
  path?: string;
  body?: string;
};

export type ParsedRecipePlugin = {
  id: string;
  action: string;
  config?: Record<string, unknown>;
};

export type ParsedRecipeTunnel = {
  mode?: string;
  remote_host?: string;
  remote_port?: number;
  local_port?: number;
  bind?: string;
  use_ssh_config?: boolean;
  ssh_config_match?: string;
  share_key?: string;
  protocol?: string;
  remote_bind?: string;
  remote_listen_port?: number;
  local_host?: string;
  local_target_port?: number;
  tun_local?: number;
  tun_remote?: number;
  remote_socat?: boolean;
};

export type ParsedRecipeAgentTransferCloud = {
  provider: string;
  bucket: string;
  prefix?: string;
  object?: string;
  region?: string;
  endpoint?: string;
};

export type ParsedRecipeCloudBackendRef = {
  kind: string;
  name?: string;
  index?: number;
};

export type ParsedRecipeAgentTransfer = {
  dest_host: string;
  source_path: string;
  dest_path: string;
  cloud: ParsedRecipeAgentTransferCloud;
  cloud_backend_ref?: ParsedRecipeCloudBackendRef;
  keep_object?: boolean;
  max_retries?: number;
  agent_remote_dir?: string;
};

export type ParsedRecipeNotifyServices = {
  http?: Record<string, never>;
  slack?: { channel_id?: string };
  telegram?: Record<string, never>;
};

export type ParsedRecipeNotify = {
  notify_subject?: string;
  message?: string;
  services?: ParsedRecipeNotifyServices;
};

export type ParsedRecipeStep = {
  id?: string;
  depends?: string[];
  host: string;
  command?: string;
  script?: ParsedRecipeFileTransfer;
  ai?: { model?: string; prompt?: string; system_prompt?: string; max_output_tokens?: number; max_input_chars?: number };
  template?: ParsedRecipeStepTemplate;
  put?: ParsedRecipeFileTransfer;
  get?: ParsedRecipeFileTransfer;
  plugin?: ParsedRecipePlugin;
  tunnel?: ParsedRecipeTunnel;
  agent_transfer?: ParsedRecipeAgentTransfer;
  notify?: ParsedRecipeNotify;
  run_as?: string;
  env?: Record<string, string>;
  max_parallel?: number;
  kv_tunnel?: boolean;
  env_from?: ParsedRecipeEnvFrom[];
  when?: string;
  retry?: ParsedRecipeStepRetry;
  hooks?: { on_success?: ParsedRecipeStep; on_failure?: ParsedRecipeStep };
};

export type ResolvedStep = {
  index: number;
  id?: string;
  depends?: string[];
  wave?: number;
  kind: string;
  host: string;
  run_as?: string;
  when?: string;
  retry?: string;
  notify?: boolean;
  preview: string;
};

export type GraphPlanNode = {
  index: number;
  id: string;
  kind: string;
  host: string;
  wave?: number;
  when?: string;
  retry?: string;
  notify?: boolean;
  kv_tunnel?: boolean;
  preview?: string;
};

export type GraphPlanEdge = {
  from: string;
  to: string;
};

export type RecipeGraphPlan = {
  type: string;
  waves?: GraphPlanNode[][];
  nodes: GraphPlanNode[];
  edges: GraphPlanEdge[];
  mermaid?: string;
};

export function isGraphRecipe(recipe: ParsedRecipe): boolean {
  return recipe.type?.trim().toLowerCase() === 'graph';
}

export type ValidationError = {
  path?: string;
  kind: 'json' | 'schema' | 'validation' | 'resolve';
  message: string;
};

export type RecentRunEntry = {
  recipe_name: string;
  recipe_path: string;
  host_count: number;
  started_at: string;
  recording_id: string;
  recipe_content_hash?: string;
  edited: boolean;
  hosts?: HostRecord[];
};

export type RecordingsRetentionInfo = {
  enabled: boolean;
  max_age?: string;
};

export type RecordingsListResponse = {
  items: RecordingListEntry[];
  file_count: number;
  total_bytes: number;
  retention?: RecordingsRetentionInfo;
};

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

export type ConfigSchemaFieldType = 'string' | 'boolean' | 'integer' | 'array' | 'object';

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
  items?: ConfigSchemaFieldSpec[];
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

export async function fetchRecentRuns(limit = 20): Promise<RecentRunEntry[]> {
  const r = await apiGet(`/api/v1/recipes/recent-runs?limit=${encodeURIComponent(limit)}`);
  const j = (await r.json().catch(() => ({}))) as { runs?: RecentRunEntry[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.runs ?? [];
}

/**
 * Validate a structured recipe payload. Returns `{plan, steps}` on success (200), or
 * `{errors}` on a 400 validation failure. Other HTTP errors throw.
 */
export async function validateRecipeContent(
  recipe: ParsedRecipe,
): Promise<{ plan: string; steps: ResolvedStep[]; graph?: RecipeGraphPlan } | { errors: ValidationError[] }> {
  const r = await apiPost('/api/v1/recipes/validate-content', { recipe_content: recipe });
  const body = (await r.json().catch(() => ({}))) as {
    plan?: string;
    steps?: ResolvedStep[];
    graph?: RecipeGraphPlan;
    errors?: ValidationError[];
    error?: string;
  };
  if (r.ok) {
    return { plan: body.plan ?? '', steps: body.steps ?? [], graph: body.graph };
  }
  if (r.status === 400) {
    return {
      errors: body.errors ?? [{ kind: 'validation', message: body.error || 'unknown error' }],
    };
  }
  throw new Error(body.error || r.statusText);
}

export async function fetchRecipeContent(path: string): Promise<string> {
  const r = await apiPost('/api/v1/recipes/view', { path });
  const j = (await r.json().catch(() => ({}))) as { content?: string; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.content ?? '';
}

/** Parse a disk recipe (.cue) into a ParsedRecipe via POST /api/v1/recipes/parse. */
export async function parseDiskRecipe(path: string): Promise<ParsedRecipe> {
  const r = await apiPost('/api/v1/recipes/parse', { path });
  const j = (await r.json().catch(() => ({}))) as { recipe?: ParsedRecipe; error?: string };
  if (!r.ok) {
    throw new Error(j.error || `parse failed: ${r.status}`);
  }
  if (!j.recipe) {
    throw new Error('parse: missing recipe in response');
  }
  return j.recipe;
}

/** List recordings; omit filters to return everything in record-dir (e.g. batch exec files use host_name batch-N). */
export async function fetchRecordingsList(filters?: {
  provider?: string;
  host_name?: string;
  host_ip?: string;
}): Promise<RecordingsListResponse> {
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
  const j = (await r.json().catch(() => ({}))) as RecordingsListResponse & { error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return {
    items: j.items || [],
    file_count: j.file_count ?? (j.items?.length ?? 0),
    total_bytes: j.total_bytes ?? 0,
    retention: j.retention,
  };
}

export async function deleteRecording(fileName: string): Promise<void> {
  const r = await apiDelete(`/api/v1/recordings/${encodeURIComponent(fileName)}`);
  const j = (await r.json().catch(() => ({}))) as { error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
}

export async function fetchTerminalAssistModels(): Promise<string[]> {
  const r = await apiGet('/api/v1/terminal-assist/models');
  const j = (await r.json().catch(() => ({}))) as { models?: string[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.models || [];
}

export async function summarizeRecording(fileName: string, model: string): Promise<string> {
  const r = await apiPost('/api/v1/recordings/summarize', { file_name: fileName, model: model.trim() });
  const j = (await r.json().catch(() => ({}))) as { reply?: string; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.reply ?? '';
}

export async function fetchRecordingsForHost(params: {
  provider: string;
  host_name: string;
  host_ip: string;
}): Promise<RecordingListEntry[]> {
  const resp = await fetchRecordingsList({
    provider: params.provider,
    host_name: params.host_name,
    host_ip: params.host_ip,
  });
  return resp.items;
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

export type CueExecRequest = {
  recipe_path?: string;
  recipe_content?: ParsedRecipe;
  execute: boolean;
  ssh_user: string;
  records: unknown[];
  env?: string[];
  record_session?: boolean;
};

export async function cueExec(body: CueExecRequest): Promise<{ plan?: string; results?: HostExecResultRow[] }> {
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
  body: CueExecRequest,
  onRow: (row: HostExecResultRow) => void,
): Promise<{ recording_id?: string }> {
  const r = await fetch('/api/v1/cue-exec?stream=1', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  const recordingId = r.headers.get('X-Honey-Recording-Id')?.trim() || undefined;
  await readNDJSON<HostExecResultRow>(r, onRow);
  return { recording_id: recordingId };
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
  signal?: AbortSignal,
): Promise<void> {
  const r = await fetch('/api/v1/files/agent-transfer?stream=1', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  await readNDJSON<AgentTransferEvent>(r, onEvent);
}

/** Bytes sent to this origin; then server may still work (e.g. SFTP to a host). */
export type FormDataUploadProgressEvent =
  | { kind: 'uploading'; loaded: number; total: number | null }
  | { kind: 'awaiting_response' };

/** Server-sent upload stream after the multipart body is stored (SFTP byte progress). */
export type UploadStreamServerEvent =
  | { phase: 'sftp_start'; total_bytes: number }
  | { phase: 'sftp'; sent_bytes: number; total_bytes: number }
  | { phase: 'error'; message?: string; result?: HostExecResultRow }
  | { phase: 'done'; results: HostExecResultRow[] };

/**
 * POST multipart to Honey with ?stream=1: XHR reports bytes to the server; response body is NDJSON
 * with SFTP progress from the Honey process. Resolves the same result list as the non-streaming upload.
 */
export function uploadFormDataWithSFTPStream(
  url: string,
  formData: FormData,
  opts: {
    onHoneyProgress?: (ev: FormDataUploadProgressEvent) => void;
    onServerEvent?: (ev: UploadStreamServerEvent) => void;
  },
): Promise<HostExecResultRow[]> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', url);
    const h = apiHeaders() as Record<string, string>;
    for (const [k, v] of Object.entries(h)) {
      xhr.setRequestHeader(k, v);
    }

    let parsePos = 0;
    let streamErr: Error | null = null;
    let doneResults: HostExecResultRow[] | null = null;

    const drain = () => {
      const text = xhr.responseText;
      while (parsePos < text.length) {
        const nl = text.indexOf('\n', parsePos);
        if (nl < 0) {
          break;
        }
        const line = text.slice(parsePos, nl).trim();
        parsePos = nl + 1;
        if (!line) {
          continue;
        }
        let row: unknown;
        try {
          row = JSON.parse(line) as unknown;
        } catch {
          continue;
        }
        if (!row || typeof row !== 'object' || !('phase' in row)) {
          continue;
        }
        const ev = row as UploadStreamServerEvent;
        opts.onServerEvent?.(ev);
        const phase = String((row as { phase: string }).phase);
        if (phase === 'error') {
          const msg = (row as { message?: string }).message?.trim() || 'upload failed';
          streamErr = new Error(msg);
        }
        if (phase === 'done') {
          doneResults = (row as { results?: HostExecResultRow[] }).results || [];
        }
      }
    };

    xhr.upload.onprogress = (ev) => {
      opts.onHoneyProgress?.({
        kind: 'uploading',
        loaded: ev.loaded,
        total: ev.lengthComputable && ev.total > 0 ? ev.total : null,
      });
    };
    xhr.upload.onloadend = () => {
      opts.onHoneyProgress?.({ kind: 'awaiting_response' });
    };
    xhr.onreadystatechange = () => {
      if (xhr.readyState >= 3) {
        drain();
      }
    };
    xhr.onprogress = () => {
      drain();
    };
    xhr.onload = () => {
      drain();
      if (xhr.status < 200 || xhr.status >= 300) {
        let msg = xhr.statusText || `HTTP ${xhr.status}`;
        try {
          const j = JSON.parse(xhr.responseText) as { error?: string };
          if (j.error) {
            msg = j.error;
          }
        } catch {
          /* ignore */
        }
        reject(new Error(msg));
        return;
      }
      if (streamErr) {
        reject(streamErr);
        return;
      }
      if (doneResults) {
        resolve(doneResults);
        return;
      }
      reject(new Error('upload stream ended without result'));
    };
    xhr.onerror = () => reject(new Error('network error'));
    xhr.send(formData);
  });
}

/** POST multipart FormData with upload progress (bytes to this origin only). Resolves parsed JSON body. */
export function uploadFormDataWithProgress(
  url: string,
  formData: FormData,
  onProgress?: (ev: FormDataUploadProgressEvent) => void,
): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', url);
    const h = apiHeaders() as Record<string, string>;
    for (const [k, v] of Object.entries(h)) {
      xhr.setRequestHeader(k, v);
    }
    xhr.upload.onprogress = (ev) => {
      onProgress?.({
        kind: 'uploading',
        loaded: ev.loaded,
        total: ev.lengthComputable && ev.total > 0 ? ev.total : null,
      });
    };
    xhr.upload.onloadend = () => {
      onProgress?.({ kind: 'awaiting_response' });
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
