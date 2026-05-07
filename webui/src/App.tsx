import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  apiGet,
  apiPost,
  apiPut,
  cueExec,
  cueExecStream,
  execOnHosts,
  execOnHostsStream,
  fetchConfigSchema,
  fetchRecordingsForHost,
  fetchRecordingsList,
  fetchRecipeContent,
  fetchRecipes,
  getToken,
  uploadFormDataWithProgress,
} from './api';
import type { ConfigUISchema, HostExecResultRow, RecipeListEntry, RecordingListEntry } from './api';
import { ConfigBackendsSection } from './ConfigBackendsSection';
import { SessionReplayModal } from './SessionReplayModal';
import { TerminalModal } from './TerminalModal';

type BackendRow = { kind: string; name: string; hint: string };

type HostRecord = {
  provider: string;
  name: string;
  primary_ip: string;
  extra_ips?: string[];
  zone?: string;
  region?: string;
  meta?: Record<string, string>;
};

type Tab = 'search' | 'backends' | 'config';
const HighlightedCode = lazy(async () => import('./HighlightedCode').then((m) => ({ default: m.HighlightedCode })));
const RawYamlEditor = lazy(async () => import('./RawYamlEditor').then((m) => ({ default: m.RawYamlEditor })));

function recordKey(rec: HostRecord): string {
  return `${rec.provider}\x1e${rec.name}\x1e${rec.primary_ip}`;
}

function recordHaystack(rec: HostRecord): string {
  const parts = [rec.provider, rec.name, rec.primary_ip, rec.zone || '', rec.region || ''];
  if (rec.extra_ips?.length) {
    parts.push(rec.extra_ips.join(' '));
  }
  if (rec.meta) {
    for (const v of Object.values(rec.meta)) {
      parts.push(v);
    }
  }
  return parts.join(' ').toLowerCase();
}

function recordIndex(records: HostRecord[], rec: HostRecord): number {
  const k = recordKey(rec);
  return records.findIndex((r) => recordKey(r) === k);
}

function detectCodeLanguage(fileName: string): 'cue' | 'yaml' {
  const lowerName = fileName.toLowerCase();
  if (lowerName.endsWith('.yaml') || lowerName.endsWith('.yml')) {
    return 'yaml';
  }
  return 'cue';
}

function CodeLoadingFallback({ code }: { code: string }) {
  return (
    <pre
      style={{
        margin: 0,
        fontSize: '0.78rem',
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-word',
        overflow: 'auto',
        padding: '0.65rem',
        border: '1px solid #2a3140',
        borderRadius: 6,
        background: '#0f1115',
      }}
    >
      {code}
    </pre>
  );
}

export function App() {
  const [tab, setTab] = useState<Tab>('search');
  const [tokenMsg, setTokenMsg] = useState('');
  const [meta, setMeta] = useState<{
    version: string;
    config_path: string;
    session_recording_available?: boolean;
    terminal_assist_available?: boolean;
  } | null>(null);
  const [backends, setBackends] = useState<BackendRow[]>([]);
  const [backErr, setBackErr] = useState<string | null>(null);

  const [name, setName] = useState('');
  const [selectedProviders, setSelectedProviders] = useState<string[]>([]);
  const [selectedBackends, setSelectedBackends] = useState<string[]>([]);
  const [providerIds, setProviderIds] = useState<string[]>([]);
  const [records, setRecords] = useState<HostRecord[]>([]);
  const [resultFilter, setResultFilter] = useState('');
  const [searchErr, setSearchErr] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);

  const [yaml, setYaml] = useState('');
  const [yamlHasLintIssue, setYamlHasLintIssue] = useState(false);
  const [cfgErr, setCfgErr] = useState<string | null>(null);
  const [cfgPath, setCfgPath] = useState<string | null>(null);
  const [cfgSchema, setCfgSchema] = useState<ConfigUISchema | null>(null);
  const [cfgSchemaErr, setCfgSchemaErr] = useState<string | null>(null);

  const [termRecord, setTermRecord] = useState<HostRecord | null>(null);
  const [replayRecord, setReplayRecord] = useState<HostRecord | null>(null);
  const [replayItems, setReplayItems] = useState<RecordingListEntry[]>([]);
  const [replayErr, setReplayErr] = useState<string | null>(null);
  const [recordWebSession, setRecordWebSession] = useState(false);
  const [sshUser, setSshUser] = useState(() => '');

  const [uploadModalOpen, setUploadModalOpen] = useState(false);
  const [uploadTargetIdx, setUploadTargetIdx] = useState(0);
  const [uploadRemote, setUploadRemote] = useState('/tmp/');
  const [uploadProgress, setUploadProgress] = useState<number | null>(null);
  const [uploadStatus, setUploadStatus] = useState('');
  const [uploadStatusIsError, setUploadStatusIsError] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const [providerMenuOpen, setProviderMenuOpen] = useState(false);
  const [backendMenuOpen, setBackendMenuOpen] = useState(false);
  const providerMenuRef = useRef<HTMLDivElement>(null);
  const backendMenuRef = useRef<HTMLDivElement>(null);

  const [selectedKeys, setSelectedKeys] = useState<Record<string, boolean>>({});
  const [pageSize, setPageSize] = useState(25);
  const [currentPage, setCurrentPage] = useState(1);
  const [execCommand, setExecCommand] = useState('');
  const [execBusy, setExecBusy] = useState(false);
  const [execErr, setExecErr] = useState<string | null>(null);
  const [execResults, setExecResults] = useState<HostExecResultRow[] | null>(null);

  const [recipes, setRecipes] = useState<RecipeListEntry[]>([]);
  const [recipesErr, setRecipesErr] = useState<string | null>(null);

  const [recipePreview, setRecipePreview] = useState<{ title: string; content: string } | null>(null);
  const [cuePlanText, setCuePlanText] = useState<string | null>(null);
  const [cueBusy, setCueBusy] = useState(false);
  const [cueErr, setCueErr] = useState<string | null>(null);
  const [cueExecResults, setCueExecResults] = useState<HostExecResultRow[] | null>(null);

  useEffect(() => {
    if (!getToken()) {
      setTokenMsg('Add ?token=… to the URL (printed when you start honey web).');
    } else {
      setTokenMsg('');
    }
  }, []);

  useEffect(() => {
    function onDocMouseDown(e: MouseEvent) {
      const t = e.target as Node;
      if (providerMenuRef.current?.contains(t)) {
        return;
      }
      if (backendMenuRef.current?.contains(t)) {
        return;
      }
      setProviderMenuOpen(false);
      setBackendMenuOpen(false);
    }
    if (providerMenuOpen || backendMenuOpen) {
      document.addEventListener('mousedown', onDocMouseDown);
      return () => document.removeEventListener('mousedown', onDocMouseDown);
    }
    return undefined;
  }, [providerMenuOpen, backendMenuOpen]);

  const loadMeta = useCallback(async () => {
    const r = await apiGet('/api/v1/meta');
    if (!r.ok) {
      setMeta(null);
      return;
    }
    const j = (await r.json()) as {
      version: string;
      config_path: string;
      session_recording_available?: boolean;
      terminal_assist_available?: boolean;
    };
    setMeta(j);
  }, []);

  useEffect(() => {
    if (!meta?.session_recording_available) {
      setRecordWebSession(false);
    }
  }, [meta?.session_recording_available]);

  useEffect(() => {
    void loadMeta();
  }, [loadMeta]);

  const loadProviders = useCallback(async () => {
    const r = await apiGet('/api/v1/providers');
    if (!r.ok) {
      setProviderIds([]);
      return;
    }
    const j = (await r.json()) as { providers?: string[] };
    setProviderIds(j.providers || []);
  }, []);

  useEffect(() => {
    void loadProviders();
  }, [loadProviders]);

  const loadBackends = useCallback(async () => {
    setBackErr(null);
    const r = await apiGet('/api/v1/backends');
    if (!r.ok) {
      const j = await r.json().catch(() => ({}));
      setBackErr((j as { error?: string }).error || r.statusText);
      setBackends([]);
      return;
    }
    const j = (await r.json()) as { backends: BackendRow[] };
    setBackends(j.backends || []);
  }, []);

  useEffect(() => {
    if (tab === 'backends' || tab === 'search') {
      void loadBackends();
    }
  }, [tab, loadBackends]);

  const loadConfig = useCallback(async () => {
    setCfgErr(null);
    const r = await apiGet('/api/v1/config');
    if (!r.ok) {
      const j = await r.json().catch(() => ({}));
      setCfgErr((j as { error?: string }).error || r.statusText);
      return;
    }
    setCfgPath(r.headers.get('X-Config-Path'));
    setYaml(await r.text());
  }, []);

  const loadConfigSchema = useCallback(async () => {
    setCfgSchemaErr(null);
    try {
      const schema = await fetchConfigSchema();
      setCfgSchema(schema.ui_schema);
    } catch (e) {
      setCfgSchema(null);
      setCfgSchemaErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    if (tab === 'config') {
      void loadConfig();
      void loadConfigSchema();
    }
  }, [tab, loadConfig, loadConfigSchema]);

  useEffect(() => {
    setUploadTargetIdx((i) => {
      if (records.length === 0) {
        return 0;
      }
      return Math.min(Math.max(0, i), records.length - 1);
    });
  }, [records]);

  const displayRecords = useMemo(() => {
    const q = resultFilter.trim().toLowerCase();
    if (!q) {
      return records;
    }
    return records.filter((rec) => recordHaystack(rec).includes(q));
  }, [records, resultFilter]);

  const totalRows = displayRecords.length;
  const totalPages = Math.max(1, Math.ceil(totalRows / pageSize));
  const pageStart = (currentPage - 1) * pageSize;
  const pageEnd = pageStart + pageSize;
  const pagedRecords = useMemo(
    () => displayRecords.slice(pageStart, pageEnd),
    [displayRecords, pageStart, pageEnd],
  );
  const showingFrom = totalRows === 0 ? 0 : pageStart + 1;
  const showingTo = totalRows === 0 ? 0 : Math.min(pageEnd, totalRows);

  useEffect(() => {
    setCurrentPage(1);
  }, [resultFilter, pageSize]);

  useEffect(() => {
    setCurrentPage((p) => Math.min(Math.max(1, p), totalPages));
  }, [totalPages]);

  const selectedRecords = useMemo(
    () => records.filter((r) => selectedKeys[recordKey(r)]),
    [records, selectedKeys],
  );

  const loadRecipes = useCallback(async () => {
    setRecipesErr(null);
    try {
      setRecipes(await fetchRecipes());
    } catch (e) {
      setRecipes([]);
      setRecipesErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    if (tab === 'search') {
      void loadRecipes();
    }
  }, [tab, loadRecipes]);

  const toggleRowSelected = (rec: HostRecord) => {
    const k = recordKey(rec);
    setSelectedKeys((prev) => {
      const next = { ...prev };
      if (next[k]) {
        delete next[k];
      } else {
        next[k] = true;
      }
      return next;
    });
  };

  const selectVisibleHosts = () => {
    setSelectedKeys((prev) => {
      const next = { ...prev };
      for (const r of displayRecords) {
        next[recordKey(r)] = true;
      }
      return next;
    });
  };

  const clearHostSelection = () => setSelectedKeys({});
  const clearExecOutput = () => {
    setExecErr(null);
    setExecResults(null);
  };
  const clearCueOutput = () => {
    setCueErr(null);
    setCuePlanText(null);
    setCueExecResults(null);
  };

  const runParallelExec = async () => {
    const cmd = execCommand.trim();
    if (!cmd || selectedRecords.length === 0) {
      setExecErr('Select at least one host and enter a command.');
      return;
    }
    setExecBusy(true);
    setExecErr(null);
    setExecResults([]);
    try {
      await execOnHostsStream(
        {
          ssh_user: sshUser.trim(),
          command: cmd,
          records: selectedRecords,
          record_session: !!(recordWebSession && meta?.session_recording_available),
        },
        (row) => setExecResults((prev) => [...(prev || []), row]),
      );
    } catch (e) {
      setExecErr(e instanceof Error ? e.message : String(e));
    } finally {
      setExecBusy(false);
    }
  };

  const openRecipePreview = async (path: string, name: string) => {
    setRecipePreview({ title: name, content: 'Loading…' });
    try {
      const content = await fetchRecipeContent(path);
      setRecipePreview({ title: name, content });
    } catch (e) {
      setRecipePreview({
        title: name,
        content: e instanceof Error ? e.message : String(e),
      });
    }
  };

  const runRecipeDryRun = async (recipePath: string) => {
    if (selectedRecords.length === 0) {
      setCueErr('Select at least one host for dry-run.');
      setCuePlanText(null);
      return;
    }
    setCueBusy(true);
    setCueErr(null);
    setCuePlanText(null);
    setCueExecResults(null);
    try {
      const { plan } = await cueExec({
        recipe_path: recipePath,
        execute: false,
        ssh_user: sshUser.trim(),
        records: selectedRecords,
        record_session: !!(recordWebSession && meta?.session_recording_available),
      });
      setCuePlanText(plan ?? '');
    } catch (e) {
      setCueErr(e instanceof Error ? e.message : String(e));
    } finally {
      setCueBusy(false);
    }
  };

  const runRecipeExecute = async (recipePath: string) => {
    if (selectedRecords.length === 0) {
      setCueErr('Select at least one host.');
      return;
    }
    if (
      !window.confirm(
        'Execute this recipe on the selected hosts? This runs real commands and file transfers on remotes (and on the web server for recipe-relative paths).',
      )
    ) {
      return;
    }
    setCueBusy(true);
    setCueErr(null);
    setCuePlanText(null);
    setCueExecResults([]);
    try {
      await cueExecStream(
        {
          recipe_path: recipePath,
          execute: true,
          ssh_user: sshUser.trim(),
          records: selectedRecords,
          record_session: !!(recordWebSession && meta?.session_recording_available),
        },
        (row) => setCueExecResults((prev) => [...(prev || []), row]),
      );
    } catch (e) {
      setCueErr(e instanceof Error ? e.message : String(e));
    } finally {
      setCueBusy(false);
    }
  };

  const runSearch = async () => {
    setSearching(true);
    setSearchErr(null);
    try {
      const r = await apiPost('/api/v1/search', {
        name,
        providers: selectedProviders.join(','),
        backends: selectedBackends.join(','),
      });
      const j = await r.json();
      if (!r.ok) {
        setSearchErr((j as { error?: string }).error || r.statusText);
        setRecords([]);
        setSelectedKeys({});
        setCurrentPage(1);
        return;
      }
      setCurrentPage(1);
      setSelectedKeys({});
      setExecResults(null);
      setExecErr(null);
      setCuePlanText(null);
      setCueExecResults(null);
      setCueErr(null);
      setRecords((j as { records: HostRecord[] }).records || []);
    } finally {
      setSearching(false);
    }
  };

  const saveConfig = async () => {
    setCfgErr(null);
    const r = await apiPut('/api/v1/config', yaml, 'application/yaml');
    const j = await r.json().catch(() => ({}));
    if (!r.ok) {
      setCfgErr((j as { error?: string }).error || r.statusText);
      return;
    }
    await loadConfig();
  };

  const closeUploadModal = () => {
    setUploadModalOpen(false);
    setUploadProgress(null);
    setUploadStatus('');
    setUploadStatusIsError(false);
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
  };

  const runUpload = async (rec: HostRecord, file: File, remotePath: string, user: string) => {
    setUploadProgress(0);
    setUploadStatusIsError(false);
    setUploadStatus('Uploading…');
    const fd = new FormData();
    fd.append('file', file);
    fd.append(
      'meta',
      JSON.stringify({
        ssh_user: user,
        remote_path: remotePath,
        record: rec,
      }),
    );
    try {
      const body = await uploadFormDataWithProgress('/api/v1/upload', fd, (pct) => setUploadProgress(pct));
      if (Array.isArray(body)) {
        const bad = body.filter(
          (r: { Success?: boolean }) => (r as { Success?: boolean }).Success === false,
        ) as { Name?: string; ErrMsg?: string }[];
        if (bad.length) {
          const msg = bad
            .map((r) => `${r.Name ?? '?'}: ${(r.ErrMsg || 'failed').trim() || 'failed'}`)
            .join('; ');
          throw new Error(msg);
        }
      }
      setUploadStatus('Upload finished.');
      setUploadProgress(100);
      if (fileInputRef.current) {
        fileInputRef.current.value = '';
      }
    } catch (e) {
      setUploadStatusIsError(true);
      setUploadStatus(e instanceof Error ? e.message : String(e));
      setUploadProgress(null);
    }
  };

  const onUploadSubmit = () => {
    const inp = fileInputRef.current;
    const file = inp?.files?.[0];
    if (!file || records.length === 0) {
      setUploadStatusIsError(true);
      setUploadStatus('Choose a file and ensure search results include a target host.');
      return;
    }
    const rec = records[uploadTargetIdx];
    if (!rec) {
      return;
    }
    const remote = uploadRemote.trim() || `/tmp/${file.name}`;
    void runUpload(rec, file, remote, sshUser.trim());
  };

  const openUploadModal = (rec?: HostRecord) => {
    if (rec && records.length > 0) {
      const idx = recordIndex(records, rec);
      setUploadTargetIdx(idx >= 0 ? idx : 0);
    }
    setUploadModalOpen(true);
    setUploadProgress(null);
    setUploadStatus('');
    setUploadStatusIsError(false);
  };

  const onDropUpload = (rec: HostRecord, files: FileList | null) => {
    if (!files?.length) {
      return;
    }
    const f = files[0];
    const idx = recordIndex(records, rec);
    setUploadTargetIdx(idx >= 0 ? idx : 0);
    setUploadRemote(`/tmp/${f.name}`);
    setUploadModalOpen(true);
    setUploadProgress(0);
    setUploadStatusIsError(false);
    setUploadStatus('Uploading…');
    void runUpload(rec, f, `/tmp/${f.name}`, sshUser.trim());
  };

  const openReplayModal = async (rec: HostRecord) => {
    setReplayErr(null);
    setReplayRecord(rec);
    setReplayItems([]);
    try {
      const items = await fetchRecordingsForHost({
        provider: rec.provider,
        host_name: rec.name,
        host_ip: rec.primary_ip,
      });
      setReplayItems(items);
      if (items.length === 0) {
        setReplayErr('No recordings found for this host.');
      }
    } catch (e) {
      setReplayErr(e instanceof Error ? e.message : String(e));
    }
  };

  const openReplayAllRecordings = async () => {
    const placeholder: HostRecord = { provider: '', name: 'All recordings', primary_ip: '' };
    setReplayErr(null);
    setReplayRecord(placeholder);
    setReplayItems([]);
    try {
      const items = await fetchRecordingsList();
      setReplayItems(items);
      if (items.length === 0) {
        setReplayErr('No files in record-dir yet.');
      }
    } catch (e) {
      setReplayErr(e instanceof Error ? e.message : String(e));
    }
  };

  const namedBackends = backends.filter((b) => b.name.trim() !== '');

  const providerSelectSize = Math.min(10, Math.max(3, providerIds.length || 1));
  const backendOptions = namedBackends.map((b) => ({
    value: b.name.trim().toLowerCase(),
    label: `${b.kind}: ${b.name}`,
  }));
  const backendSelectSize = Math.min(10, Math.max(3, backendOptions.length || 1));

  const dropdownPanelStyle = {
    position: 'absolute' as const,
    top: '100%',
    left: 0,
    marginTop: 4,
    zIndex: 40,
    minWidth: 260,
    maxWidth: 360,
    padding: '0.5rem',
    background: '#1e1e1e',
    border: '1px solid #555',
    borderRadius: 6,
    boxShadow: '0 8px 24px rgba(0,0,0,0.45)',
  };

  const uploadModal = uploadModalOpen ? (
    <div className="modal-backdrop" role="presentation">
      <div
        className="modal"
        style={{ height: 'auto', maxHeight: '90vh', width: 'min(480px, 94vw)' }}
        role="dialog"
        aria-labelledby="upload-modal-title"
      >
        <header>
          <strong id="upload-modal-title">SFTP upload</strong>
          <button type="button" onClick={() => closeUploadModal()}>
            Close
          </button>
        </header>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.65rem', padding: '0.25rem 0' }}>
          <label style={{ fontSize: '0.85rem' }}>
            Target host
            <select
              style={{ display: 'block', width: '100%', marginTop: 4 }}
              value={records.length ? uploadTargetIdx : 0}
              disabled={records.length === 0}
              onChange={(e) => setUploadTargetIdx(Number(e.target.value))}
            >
              {records.length === 0 ? (
                <option value={0}>Run a search first</option>
              ) : (
                records.map((rec, i) => (
                  <option key={recordKey(rec)} value={i}>
                    {rec.name} ({rec.provider}) — {rec.primary_ip}
                  </option>
                ))
              )}
            </select>
          </label>
          <label style={{ fontSize: '0.85rem' }}>
            File
            <input ref={fileInputRef} type="file" style={{ display: 'block', marginTop: 4 }} />
          </label>
          <label style={{ fontSize: '0.85rem' }}>
            Remote path
            <input
              style={{ display: 'block', width: '100%', marginTop: 4, fontFamily: 'monospace' }}
              value={uploadRemote}
              onChange={(e) => setUploadRemote(e.target.value)}
              placeholder="/tmp/filename"
            />
          </label>
          <p style={{ fontSize: '0.8rem', opacity: 0.8, margin: 0 }}>
            SSH user comes from the field next to Search on the main screen.
          </p>
          <button type="button" className="primary" disabled={records.length === 0} onClick={() => onUploadSubmit()}>
            Upload
          </button>
          {uploadProgress !== null ? (
            <progress value={uploadProgress} max={100} style={{ width: '100%' }}>
              {uploadProgress}%
            </progress>
          ) : null}
          {uploadStatus ? (
            <p
              style={{
                fontSize: '0.85rem',
                margin: 0,
                color: uploadStatusIsError ? '#f66' : 'rgba(230,230,230,0.95)',
                whiteSpace: 'pre-wrap',
                opacity: uploadStatusIsError ? 1 : 0.9,
              }}
            >
              {uploadStatus}
            </p>
          ) : null}
        </div>
      </div>
    </div>
  ) : null;

  return (
    <main>
      <h1>Honey</h1>
      {tokenMsg ? <p style={{ color: '#f5a623' }}>{tokenMsg}</p> : null}
      {meta ? (
        <p style={{ fontSize: '0.85rem', opacity: 0.85 }}>
          v{meta.version}
          {meta.config_path ? ` · config: ${meta.config_path}` : ''}
        </p>
      ) : null}

      <nav className="tabs">
        <button type="button" className={tab === 'search' ? 'active' : ''} onClick={() => setTab('search')}>
          Search
        </button>
        <button type="button" className={tab === 'backends' ? 'active' : ''} onClick={() => setTab('backends')}>
          Backends
        </button>
        <button type="button" className={tab === 'config' ? 'active' : ''} onClick={() => setTab('config')}>
          Config
        </button>
      </nav>

      {tab === 'search' ? (
        <section>
          <div style={{ display: 'flex', gap: '0.75rem', flexWrap: 'wrap', marginBottom: '0.75rem', alignItems: 'flex-start' }}>
            <input placeholder="Name contains" value={name} onChange={(e) => setName(e.target.value)} />
            <input placeholder="SSH user" value={sshUser} onChange={(e) => setSshUser(e.target.value)} />
            <label
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '0.45rem',
                paddingTop: '0.35rem',
                fontSize: '0.85rem',
                opacity: meta?.session_recording_available ? 1 : 0.65,
                cursor: meta?.session_recording_available ? 'pointer' : 'not-allowed',
              }}
              title={
                meta?.session_recording_available
                  ? 'When checked, record new SSH/K8s terminal sessions, parallel command runs, and CUE recipe runs (dry-run and execute) to the server record-dir.'
                  : 'Recording unavailable: start honey web with --record-dir to enable.'
              }
            >
              <input
                type="checkbox"
                checked={recordWebSession}
                disabled={!meta?.session_recording_available}
                onChange={(e) => setRecordWebSession(e.target.checked)}
              />
              Record sessions
            </label>
            <button type="button" className="primary" disabled={searching} onClick={() => void runSearch()}>
              {searching ? 'Searching…' : 'Search'}
            </button>
          </div>

          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.75rem', marginBottom: '0.75rem', alignItems: 'flex-start' }}>
            <div ref={providerMenuRef} style={{ position: 'relative' }}>
              <button
                type="button"
                onClick={() => {
                  setProviderMenuOpen((o) => !o);
                  setBackendMenuOpen(false);
                }}
              >
                Providers{' '}
                {selectedProviders.length ? `(${selectedProviders.length} selected)` : '(all)'} ▾
              </button>
              {providerMenuOpen ? (
                <div style={dropdownPanelStyle}>
                  <div style={{ fontSize: '0.75rem', opacity: 0.85, marginBottom: 6 }}>Hold Ctrl/Cmd to multi-select</div>
                  <select
                    multiple
                    size={providerSelectSize}
                    value={selectedProviders}
                    style={{ width: '100%', fontFamily: 'inherit' }}
                    onChange={(e) => {
                      const next = Array.from(e.target.selectedOptions, (o) => o.value).sort();
                      setSelectedProviders(next);
                    }}
                  >
                    {providerIds.map((id) => (
                      <option key={id} value={id}>
                        {id}
                      </option>
                    ))}
                  </select>
                  <button type="button" style={{ marginTop: 8 }} onClick={() => setSelectedProviders([])}>
                    Clear selection
                  </button>
                </div>
              ) : null}
            </div>

            <div ref={backendMenuRef} style={{ position: 'relative' }}>
              <button
                type="button"
                onClick={() => {
                  setBackendMenuOpen((o) => !o);
                  setProviderMenuOpen(false);
                }}
              >
                Backends {selectedBackends.length ? `(${selectedBackends.length} selected)` : '(all)'} ▾
              </button>
              {backendMenuOpen ? (
                <div style={dropdownPanelStyle}>
                  <div style={{ fontSize: '0.75rem', opacity: 0.85, marginBottom: 6 }}>Hold Ctrl/Cmd to multi-select</div>
                  <select
                    multiple
                    size={backendSelectSize}
                    value={selectedBackends}
                    style={{ width: '100%', fontFamily: 'inherit' }}
                    onChange={(e) => {
                      const next = Array.from(e.target.selectedOptions, (o) => o.value).sort();
                      setSelectedBackends(next);
                    }}
                  >
                    {backendOptions.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </select>
                  <button type="button" style={{ marginTop: 8 }} onClick={() => setSelectedBackends([])}>
                    Clear selection
                  </button>
                </div>
              ) : null}
            </div>
            {meta?.session_recording_available ? (
              <button type="button" onClick={() => void openReplayAllRecordings()}>
                Browse recordings
              </button>
            ) : null}
          </div>

          {searchErr ? <p style={{ color: '#f66' }}>{searchErr}</p> : null}

          <div
            style={{
              marginBottom: '0.75rem',
              padding: '0.65rem',
              border: '1px solid #2a3140',
              borderRadius: 8,
              background: '#14171c',
            }}
          >
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.5rem', alignItems: 'center', marginBottom: '0.5rem' }}>
              <span style={{ fontSize: '0.85rem' }}>
                Selected hosts: <strong>{selectedRecords.length}</strong>
              </span>
              <button type="button" onClick={() => selectVisibleHosts()} disabled={displayRecords.length === 0}>
                Select visible
              </button>
              <button type="button" onClick={() => clearHostSelection()}>
                Clear selection
              </button>
            </div>
            <label style={{ fontSize: '0.85rem', display: 'block', marginBottom: 4 }}>
              Command (shell on each selected host)
              <textarea
                style={{
                  display: 'block',
                  width: '100%',
                  marginTop: 4,
                  minHeight: '4.5rem',
                  fontFamily: 'monospace',
                  fontSize: '0.85rem',
                }}
                value={execCommand}
                onChange={(e) => setExecCommand(e.target.value)}
                placeholder="e.g. uname -a"
              />
            </label>
            <button
              type="button"
              className="primary"
              disabled={execBusy || selectedRecords.length === 0 || !execCommand.trim()}
              onClick={() => void runParallelExec()}
            >
              {execBusy ? 'Running…' : `Run on ${selectedRecords.length} host(s)`}
            </button>
            <button type="button" style={{ marginLeft: '0.5rem' }} onClick={() => clearExecOutput()}>
              Clear results
            </button>
            {execErr ? <p style={{ color: '#f66', marginTop: '0.5rem', marginBottom: 0 }}>{execErr}</p> : null}
            {execResults ? (
              <div style={{ marginTop: '0.65rem', overflowX: 'auto' }}>
                <table style={{ fontSize: '0.8rem' }}>
                  <thead>
                    <tr>
                      <th>Host</th>
                      <th>OK</th>
                      <th>Exit</th>
                      <th>Output / error</th>
                    </tr>
                  </thead>
                  <tbody>
                    {execResults.length === 0 ? (
                      <tr>
                        <td colSpan={4} style={{ opacity: 0.8 }}>
                          {execBusy ? 'Waiting for results…' : 'No results.'}
                        </td>
                      </tr>
                    ) : null}
                    {execResults.map((row, i) => (
                      <tr key={`${row.Name}-${i}`}>
                        <td>{row.Name}</td>
                        <td>{row.Success ? 'yes' : 'no'}</td>
                        <td>{row.ExitCode}</td>
                        <td style={{ maxWidth: 420, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                          {row.ErrMsg ? <span style={{ color: '#f66' }}>{row.ErrMsg}</span> : null}
                          {row.ErrMsg && row.Output ? '\n' : null}
                          {row.Output}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : null}
          </div>

          <div style={{ marginBottom: '0.5rem' }}>
            <input
              placeholder="Filter results (provider, name, IP, zone, meta…)"
              value={resultFilter}
              onChange={(e) => setResultFilter(e.target.value)}
              style={{ width: 'min(100%, 420px)' }}
            />
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.6rem', alignItems: 'center', marginTop: '0.4rem' }}>
              <span style={{ fontSize: '0.8rem', opacity: 0.75 }}>
                Showing {showingFrom}-{showingTo} of {totalRows} (total results: {records.length})
              </span>
              <label style={{ fontSize: '0.8rem', opacity: 0.9 }}>
                Rows per page{' '}
                <select
                  value={pageSize}
                  onChange={(e) => setPageSize(Number(e.target.value))}
                  style={{ marginLeft: 4 }}
                >
                  <option value={25}>25</option>
                  <option value={50}>50</option>
                  <option value={100}>100</option>
                </select>
              </label>
              <button type="button" disabled={currentPage <= 1} onClick={() => setCurrentPage((p) => p - 1)}>
                Prev
              </button>
              <span style={{ fontSize: '0.8rem' }}>
                Page {currentPage} of {totalPages}
              </span>
              <button type="button" disabled={currentPage >= totalPages} onClick={() => setCurrentPage((p) => p + 1)}>
                Next
              </button>
            </div>
          </div>

          <div style={{ overflowX: 'auto' }} onDragOver={(e) => e.preventDefault()}>
            <table>
              <thead>
                <tr>
                  <th style={{ width: 36 }}>Sel.</th>
                  <th>Provider</th>
                  <th>Name</th>
                  <th>IP</th>
                  <th>Zone</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {pagedRecords.map((rec) => (
                  <tr
                    key={recordKey(rec)}
                    onDragOver={(e) => e.preventDefault()}
                    onDrop={(e) => {
                      e.preventDefault();
                      onDropUpload(rec, e.dataTransfer.files);
                    }}
                  >
                    <td>
                      <input
                        type="checkbox"
                        checked={!!selectedKeys[recordKey(rec)]}
                        onChange={() => toggleRowSelected(rec)}
                        aria-label={`Select ${rec.name}`}
                      />
                    </td>
                    <td>{rec.provider}</td>
                    <td>{rec.name}</td>
                    <td>{rec.primary_ip}</td>
                    <td>{rec.zone || ''}</td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      <button type="button" onClick={() => setTermRecord(rec)}>
                        Terminal
                      </button>{' '}
                      <button type="button" onClick={() => openUploadModal(rec)}>
                        Upload
                      </button>
                      {meta?.session_recording_available ? (
                        <>
                          {' '}
                          <button type="button" onClick={() => void openReplayModal(rec)}>
                            Play
                          </button>
                        </>
                      ) : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <details style={{ marginTop: '0.75rem', marginBottom: '0.75rem' }}>
            <summary style={{ cursor: 'pointer', fontWeight: 600 }}>CUE recipes (default config dirs)</summary>
            <p style={{ fontSize: '0.8rem', opacity: 0.85, marginTop: '0.5rem' }}>
              Uses hosts currently <strong>selected</strong> in the table above. Paths come from the server&apos;s default recipe directories (same as CLI).
            </p>
            {recipesErr ? <p style={{ color: '#f66' }}>{recipesErr}</p> : null}
            {recipes.length === 0 && !recipesErr ? (
              <p style={{ fontSize: '0.85rem', opacity: 0.8 }}>No .cue files found under default recipe dirs.</p>
            ) : (
              <ul style={{ listStyle: 'none', padding: 0, margin: '0.5rem 0 0' }}>
                {recipes.map((rp) => (
                  <li
                    key={rp.path}
                    style={{
                      display: 'flex',
                      flexWrap: 'wrap',
                      gap: '0.35rem',
                      alignItems: 'center',
                      padding: '0.35rem 0',
                      borderBottom: '1px solid #2a3140',
                    }}
                  >
                    <code style={{ fontSize: '0.8rem' }}>{rp.name}</code>
                    <span style={{ fontSize: '0.75rem', opacity: 0.65 }}>{rp.path}</span>
                    <button type="button" onClick={() => void openRecipePreview(rp.path, rp.name)}>
                      View
                    </button>
                    <button type="button" disabled={cueBusy} onClick={() => void runRecipeDryRun(rp.path)}>
                      Dry-run
                    </button>
                    <button type="button" disabled={cueBusy} onClick={() => void runRecipeExecute(rp.path)}>
                      Execute
                    </button>
                  </li>
                ))}
              </ul>
            )}
            {cueErr ? <p style={{ color: '#f66', marginTop: '0.5rem' }}>{cueErr}</p> : null}
            <button type="button" onClick={() => clearCueOutput()}>
              Clear recipe output
            </button>
            {cuePlanText !== null ? (
              <div style={{ marginTop: '0.5rem' }}>
                <strong style={{ fontSize: '0.85rem' }}>Dry-run plan</strong>
                <pre
                  style={{
                    marginTop: 4,
                    maxHeight: 280,
                    overflow: 'auto',
                    fontSize: '0.75rem',
                    background: '#0f1115',
                    padding: '0.5rem',
                    borderRadius: 6,
                    border: '1px solid #2a3140',
                  }}
                >
                  {cuePlanText}
                </pre>
              </div>
            ) : null}
            {cueExecResults ? (
              <div style={{ marginTop: '0.65rem', overflowX: 'auto' }}>
                <strong style={{ fontSize: '0.85rem' }}>Execute results</strong>
                <table style={{ fontSize: '0.78rem', marginTop: 6 }}>
                  <thead>
                    <tr>
                      <th>Step / host</th>
                      <th>OK</th>
                      <th>Exit</th>
                      <th>Output / error</th>
                    </tr>
                  </thead>
                  <tbody>
                    {cueExecResults.length === 0 ? (
                      <tr>
                        <td colSpan={4} style={{ opacity: 0.8 }}>
                          {cueBusy ? 'Waiting for results…' : 'No results.'}
                        </td>
                      </tr>
                    ) : null}
                    {cueExecResults.map((row, i) => (
                      <tr key={`${row.Name}-${i}`}>
                        <td>{row.Name}</td>
                        <td>{row.Success ? 'yes' : 'no'}</td>
                        <td>{row.ExitCode}</td>
                        <td style={{ maxWidth: 400, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                          {row.ErrMsg ? <span style={{ color: '#f66' }}>{row.ErrMsg}</span> : null}
                          {row.ErrMsg && row.Output ? '\n' : null}
                          {row.Output}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : null}
          </details>

          <p style={{ fontSize: '0.8rem', opacity: 0.75 }}>
            Use row <strong>Upload</strong> for the file dialog. Drop a file on a row to upload to <code>/tmp/&lt;filename&gt;</code>{' '}
            (opens progress in the upload window).
          </p>
        </section>
      ) : null}

      {tab === 'backends' ? (
        <section>
          {backErr ? <p style={{ color: '#f66' }}>{backErr}</p> : null}
          <table>
            <thead>
              <tr>
                <th>Kind</th>
                <th>Name</th>
                <th>Hint</th>
              </tr>
            </thead>
            <tbody>
              {backends.map((b) => (
                <tr key={`${b.kind}-${b.name}`}>
                  <td>{b.kind}</td>
                  <td>{b.name}</td>
                  <td>{b.hint}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ) : null}

      {tab === 'config' ? (
        <section>
          {cfgErr ? <p style={{ color: '#f66' }}>{cfgErr}</p> : null}
          {cfgSchemaErr ? <p style={{ color: '#f5a623' }}>Schema warning: {cfgSchemaErr}</p> : null}
          {cfgPath ? <p style={{ fontSize: '0.85rem' }}>Path: {cfgPath}</p> : null}
          <h2 style={{ fontSize: '1.1rem' }}>Raw YAML</h2>
          <Suspense
            fallback={
              <textarea
                style={{ width: '100%', minHeight: '420px', fontFamily: 'monospace', fontSize: '0.85rem' }}
                value={yaml}
                onChange={(e) => setYaml(e.target.value)}
              />
            }
          >
            <RawYamlEditor
              value={yaml}
              onChange={setYaml}
              schema={cfgSchema}
              onSave={() => {
                if (!yamlHasLintIssue) {
                  void saveConfig();
                }
              }}
              onLintStateChange={setYamlHasLintIssue}
            />
          </Suspense>
          <div style={{ marginTop: '0.5rem' }}>
            <button type="button" className="primary" disabled={yamlHasLintIssue} onClick={() => void saveConfig()}>
              Save YAML
            </button>
            <button type="button" style={{ marginLeft: '0.5rem' }} onClick={() => void loadConfig()}>
              Reload
            </button>
            <button type="button" style={{ marginLeft: '0.5rem' }} onClick={() => void loadConfigSchema()}>
              Reload schema
            </button>
          </div>
          {yamlHasLintIssue ? (
            <p style={{ color: '#f5a623', marginTop: '0.4rem' }}>
              Fix YAML diagnostics (warnings and errors) before saving.
            </p>
          ) : null}
          <ConfigBackendsSection schema={cfgSchema} onSaved={() => void loadConfig()} />
        </section>
      ) : null}

      {recipePreview ? (
        <div className="modal-backdrop" role="presentation">
          <div
            className="modal"
            role="dialog"
            aria-label="Recipe preview"
            style={{ width: 'min(720px, 96vw)', maxHeight: '88vh', display: 'flex', flexDirection: 'column' }}
          >
            <header>
              <strong>{recipePreview.title}</strong>
              <button type="button" onClick={() => setRecipePreview(null)}>
                Close
              </button>
            </header>
            <Suspense fallback={<CodeLoadingFallback code={recipePreview.content} />}>
              <HighlightedCode
                className="recipe-preview-code"
                code={recipePreview.content}
                language={detectCodeLanguage(recipePreview.title)}
              />
            </Suspense>
          </div>
        </div>
      ) : null}

      {termRecord ? (
        <TerminalModal
          record={termRecord}
          sshUser={sshUser}
          recordSession={recordWebSession && !!meta?.session_recording_available}
          assistAvailable={!!meta?.terminal_assist_available}
          onClose={() => setTermRecord(null)}
        />
      ) : null}
      {replayRecord ? (
        replayItems.length > 0 ? (
          <SessionReplayModal record={replayRecord} recordings={replayItems} onClose={() => setReplayRecord(null)} />
        ) : (
          <div className="modal-backdrop" role="presentation">
            <div className="modal" role="dialog" style={{ height: 'auto', width: 'min(520px, 94vw)' }}>
              <header>
                <strong>Session replay</strong>
                <button type="button" onClick={() => setReplayRecord(null)}>
                  Close
                </button>
              </header>
              <p style={{ color: replayErr ? '#f66' : 'inherit' }}>{replayErr || 'No recordings found.'}</p>
            </div>
          </div>
        )
      ) : null}
      {uploadModal}
    </main>
  );
}
