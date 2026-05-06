import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { apiGet, apiPost, apiPut, getToken, uploadFormDataWithProgress } from './api';
import { ConfigBackendsSection } from './ConfigBackendsSection';
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

export function App() {
  const [tab, setTab] = useState<Tab>('search');
  const [tokenMsg, setTokenMsg] = useState('');
  const [meta, setMeta] = useState<{ version: string; config_path: string } | null>(null);
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
  const [cfgErr, setCfgErr] = useState<string | null>(null);
  const [cfgPath, setCfgPath] = useState<string | null>(null);

  const [termRecord, setTermRecord] = useState<HostRecord | null>(null);
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
    const j = (await r.json()) as { version: string; config_path: string };
    setMeta(j);
  }, []);

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

  useEffect(() => {
    if (tab === 'config') {
      void loadConfig();
    }
  }, [tab, loadConfig]);

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
        return;
      }
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
            <input placeholder="SSH user for terminal/upload" value={sshUser} onChange={(e) => setSshUser(e.target.value)} />
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
          </div>

          {searchErr ? <p style={{ color: '#f66' }}>{searchErr}</p> : null}

          <div style={{ marginBottom: '0.5rem' }}>
            <input
              placeholder="Filter results (provider, name, IP, zone, meta…)"
              value={resultFilter}
              onChange={(e) => setResultFilter(e.target.value)}
              style={{ width: 'min(100%, 420px)' }}
            />
            <span style={{ fontSize: '0.8rem', opacity: 0.75, marginLeft: '0.5rem' }}>
              Showing {displayRecords.length} of {records.length}
            </span>
          </div>

          <div style={{ overflowX: 'auto' }} onDragOver={(e) => e.preventDefault()}>
            <table>
              <thead>
                <tr>
                  <th>Provider</th>
                  <th>Name</th>
                  <th>IP</th>
                  <th>Zone</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {displayRecords.map((rec) => (
                  <tr
                    key={recordKey(rec)}
                    onDragOver={(e) => e.preventDefault()}
                    onDrop={(e) => {
                      e.preventDefault();
                      onDropUpload(rec, e.dataTransfer.files);
                    }}
                  >
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
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
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
          {cfgPath ? <p style={{ fontSize: '0.85rem' }}>Path: {cfgPath}</p> : null}
          <h2 style={{ fontSize: '1.1rem' }}>Raw YAML</h2>
          <textarea style={{ width: '100%', minHeight: '420px', fontFamily: 'monospace', fontSize: '0.85rem' }} value={yaml} onChange={(e) => setYaml(e.target.value)} />
          <div style={{ marginTop: '0.5rem' }}>
            <button type="button" className="primary" onClick={() => void saveConfig()}>
              Save YAML
            </button>
            <button type="button" style={{ marginLeft: '0.5rem' }} onClick={() => void loadConfig()}>
              Reload
            </button>
          </div>
          <ConfigBackendsSection onSaved={() => void loadConfig()} />
        </section>
      ) : null}

      {termRecord ? (
        <TerminalModal record={termRecord} sshUser={sshUser} onClose={() => setTermRecord(null)} />
      ) : null}
      {uploadModal}
    </main>
  );
}
