import { Suspense, lazy, useCallback, useEffect, useMemo, useState } from 'react';
import {
  Layout, Menu, Typography, Modal, Button, Input, Select, Alert, Space, Spin,
} from 'antd';
import type { MenuProps } from 'antd';
import {
  SearchOutlined, FileOutlined, CloudOutlined, SettingOutlined,
  PlayCircleOutlined, ApiOutlined, AppstoreOutlined, DatabaseOutlined,
} from '@ant-design/icons';
import {
  apiGet,
  fetchRecordingsForHost,
  fetchRecordingsList,
  fetchRecipeContent,
  getToken,
  recipeAssist,
  startTunnel,
  fetchHostPorts,
} from './api';
import type { RecordingListEntry, RecordingsListResponse } from './api';
import { recordKey } from './HostPicker';
import type { HostRecord } from './HostPicker';
import { RecipesTab } from './RecipesTab';
import { AppsTab } from './AppsTab';
import { BackendsTab } from './tabs/BackendsTab';
import { FilesTab } from './tabs/FilesTab';
import { TunnelsTab } from './tabs/TunnelsTab';
import { ConfigTab } from './tabs/ConfigTab';
import { ApiDocsTab } from './tabs/ApiDocsTab';
import { SearchTab } from './tabs/SearchTab';
import { SessionReplayModal } from './SessionReplayModal';
import {
  TerminalTabsModal,
  type PveConsoleMode,
  type TerminalSessionConfig,
  type TrueNASConsoleMode,
} from './TerminalModal';

type BackendRow = { kind: string; name: string; hint: string };


type Tab = 'search' | 'files' | 'backends' | 'config' | 'recipes' | 'tunnels' | 'apps' | 'api-docs';
const HighlightedCode = lazy(async () => import('./HighlightedCode').then((m) => ({ default: m.HighlightedCode })));
const AiMarkdown = lazy(async () => import('./AiMarkdown').then((m) => ({ default: m.AiMarkdown })));

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
  const initParams = new URLSearchParams(window.location.search);
  
  const [tab, setTab] = useState<Tab>(() => {
    const val = initParams.get('tab');
    if (
      val === 'search' ||
      val === 'files' ||
      val === 'backends' ||
      val === 'config' ||
      val === 'recipes' ||
      val === 'tunnels' ||
      val === 'apps' ||
      val === 'api-docs'
    ) {
      return val as Tab;
    }
    return 'search';
  });
  const [tokenMsg, setTokenMsg] = useState('');
  const [meta, setMeta] = useState<{
    version: string;
    config_path: string;
    session_recording_available?: boolean;
    terminal_assist_available?: boolean;
  } | null>(null);
  const [backends, setBackends] = useState<BackendRow[]>([]);
  const [backErr, setBackErr] = useState<string | null>(null);

  const [selectedProviders, setSelectedProviders] = useState<string[]>(() => {
    const val = initParams.get('selectedProviders');
    return val ? val.split(',') : [];
  });
  const [selectedBackends, setSelectedBackends] = useState<string[]>(() => {
    const val = initParams.get('selectedBackends');
    return val ? val.split(',') : [];
  });
  const [providerIds, setProviderIds] = useState<string[]>([]);
  const [records, setRecords] = useState<HostRecord[]>([]);
  const [sshUser, setSshUser] = useState(() => initParams.get('sshUser') || '');
  const [selectedKeys, setSelectedKeys] = useState<Record<string, boolean>>(() => {
    const val = initParams.get('selectedKeys');
    if (!val) return {};
    return val.split(',').reduce((acc, key) => ({ ...acc, [key]: true }), {});
  });

  const [terminals, setTerminals] = useState<TerminalSessionConfig[]>(() => {
    const val = initParams.get('terminals');
    if (!val) return [];
    try {
      return val.split(',').map(part => {
        const [id, key, pve, truenasConsole] = part.split('|');
        const sessionRec = sessionStorage.getItem(`honey_term_${id}`);
        const record = sessionRec ? JSON.parse(sessionRec) : { _key: key, provider: 'loading', name: 'loading', primary_ip: '' };
        return {
          id: id || Math.random().toString(36).slice(2),
          record,
          pve: (pve as PveConsoleMode) || 'serial',
          truenasConsole: (truenasConsole as TrueNASConsoleMode) || 'ssh',
        };
      });
    } catch {
      return [];
    }
  });
  const [activeTermId, setActiveTermId] = useState<string | null>(() => initParams.get('activeTermId'));
  const [isTerminalModalOpen, setIsTerminalModalOpen] = useState(() => initParams.get('isTerminalModalOpen') === 'true');
  
  const [tunnelOpen, setTunnelOpen] = useState<{ record: HostRecord } | null>(null);
  const [tunnelLocalPort, setTunnelLocalPort] = useState('');
  const [tunnelRemotePort, setTunnelRemotePort] = useState('');
  const [tunnelRemoteHost, setTunnelRemoteHost] = useState('');
  const [tunnelBusy, setTunnelBusy] = useState(false);
  const [tunnelErr, setTunnelErr] = useState<string | null>(null);
  const [tunnelPorts, setTunnelPorts] = useState<string[]>([]);
  const [tunnelPortsLoading, setTunnelPortsLoading] = useState(false);
  const [tunnelPortsErr, setTunnelPortsErr] = useState<string | null>(null);


  const [replayRecord, setReplayRecord] = useState<HostRecord | null>(null);
  const [replayItems, setReplayItems] = useState<RecordingListEntry[]>([]);
  const [replayListMeta, setReplayListMeta] = useState<Pick<
    RecordingsListResponse,
    'file_count' | 'total_bytes' | 'retention'
  > | null>(null);
  const [replayErr, setReplayErr] = useState<string | null>(null);

  const [recipePreview, setRecipePreview] = useState<{ title: string; content: string } | null>(null);

  const [recipeAssistOpen, setRecipeAssistOpen] = useState<{ path: string; name: string } | null>(null);
  const [recipeAssistModels, setRecipeAssistModels] = useState<string[]>([]);
  const [recipeAssistModelsLoading, setRecipeAssistModelsLoading] = useState(false);
  const [recipeAssistModelsErr, setRecipeAssistModelsErr] = useState<string | null>(null);
  const [recipeAssistSelectedModel, setRecipeAssistSelectedModel] = useState('');
  const [recipeAssistPrompt, setRecipeAssistPrompt] = useState('');
  const [recipeAssistBusy, setRecipeAssistBusy] = useState(false);
  const [recipeAssistErr, setRecipeAssistErr] = useState<string | null>(null);
  const [recipeAssistReply, setRecipeAssistReply] = useState('');

  useEffect(() => {
    if (!getToken()) {
      setTokenMsg('Add ?token=… to the URL (printed when you start honey web).');
    } else {
      setTokenMsg('');
    }
  }, []);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const originalString = params.toString();

    // Tab
    if (tab && tab !== 'search') params.set('tab', tab);
    else params.delete('tab');

    // Providers
    if (selectedProviders.length > 0) params.set('selectedProviders', selectedProviders.join(','));
    else params.delete('selectedProviders');

    // Backends
    if (selectedBackends.length > 0) params.set('selectedBackends', selectedBackends.join(','));
    else params.delete('selectedBackends');

    // Selected Keys
    const keys = Object.keys(selectedKeys).filter((k) => selectedKeys[k]);
    if (keys.length > 0) params.set('selectedKeys', keys.join(','));
    else params.delete('selectedKeys');

    // SSH User
    if (sshUser) params.set('sshUser', sshUser);
    else params.delete('sshUser');

    // Terminals
    if (terminals.length > 0) {
      const joined = terminals.map(t => {
        const rKey = (t.record as any)._key || recordKey(t.record);
        return `${t.id}|${rKey}|${t.pve || 'serial'}|${t.truenasConsole || 'ssh'}`;
      }).join(',');
      params.set('terminals', joined);
    } else {
      params.delete('terminals');
    }

    if (activeTermId) params.set('activeTermId', activeTermId);
    else params.delete('activeTermId');

    if (isTerminalModalOpen) params.set('isTerminalModalOpen', 'true');
    else params.delete('isTerminalModalOpen');

    if (params.toString() !== originalString) {
      window.history.replaceState(null, '', `?${params.toString()}`);
    }
  }, [
    tab,
    selectedProviders,
    selectedBackends,
    selectedKeys,
    sshUser,
    terminals,
    activeTermId,
    isTerminalModalOpen,
  ]);

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
    if (tab === 'backends' || tab === 'search' || tab === 'files') {
      void loadBackends();
    }
  }, [tab, loadBackends]);

  const selectedRecords = useMemo(
    () => records.filter((r) => selectedKeys[recordKey(r)]),
    [records, selectedKeys],
  );

  const menuItems: MenuProps['items'] = [
    { key: 'search',   icon: <SearchOutlined />,    label: 'Search' },
    { key: 'files',    icon: <FileOutlined />,       label: 'Files' },
    { key: 'backends', icon: <CloudOutlined />,      label: 'Backends' },
    { key: 'config',   icon: <SettingOutlined />,    label: 'Config' },
    { key: 'recipes',  icon: <PlayCircleOutlined />, label: 'Recipes' },
    { key: 'tunnels',  icon: <ApiOutlined />,        label: 'Tunnels' },
    { key: 'apps',     icon: <DatabaseOutlined />,   label: 'Apps & Proxies' },
    { key: 'api-docs', icon: <AppstoreOutlined />,   label: 'API Docs' },
  ];

  useEffect(() => {
    if (!recipeAssistOpen || !meta?.terminal_assist_available) {
      return undefined;
    }
    let cancelled = false;
    setRecipeAssistModelsLoading(true);
    setRecipeAssistModelsErr(null);
    void (async () => {
      try {
        const r = await apiGet('/api/v1/terminal-assist/models');
        const j = (await r.json().catch(() => ({}))) as { models?: string[]; error?: string };
        if (cancelled) {
          return;
        }
        if (!r.ok) {
          setRecipeAssistModels([]);
          setRecipeAssistSelectedModel('');
          setRecipeAssistModelsErr(j.error || r.statusText || 'Could not load models');
          return;
        }
        const list = Array.isArray(j.models) ? j.models : [];
        setRecipeAssistModels(list);
        setRecipeAssistSelectedModel(list[0] ?? '');
        if (list.length === 0) {
          setRecipeAssistModelsErr('No models returned by the provider.');
        }
      } catch (e) {
        if (!cancelled) {
          setRecipeAssistModels([]);
          setRecipeAssistSelectedModel('');
          setRecipeAssistModelsErr(e instanceof Error ? e.message : String(e));
        }
      } finally {
        if (!cancelled) {
          setRecipeAssistModelsLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [recipeAssistOpen, meta?.terminal_assist_available]);

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

  const openRecipeAssist = (path: string, name: string) => {
    setRecipeAssistReply('');
    setRecipeAssistErr(null);
    setRecipeAssistPrompt('');
    setRecipeAssistOpen({ path, name });
  };

  const closeRecipeAssist = () => {
    setRecipeAssistOpen(null);
    setRecipeAssistReply('');
    setRecipeAssistErr(null);
    setRecipeAssistBusy(false);
  };

  const submitRecipeAssist = async () => {
    if (!recipeAssistOpen || !recipeAssistSelectedModel.trim()) {
      return;
    }
    setRecipeAssistBusy(true);
    setRecipeAssistErr(null);
    setRecipeAssistReply('');
    try {
      const { reply } = await recipeAssist({
        recipe_path: recipeAssistOpen.path,
        model: recipeAssistSelectedModel.trim(),
        user_prompt: recipeAssistPrompt.trim(),
        ssh_user: sshUser.trim(),
        records: selectedRecords,
      });
      setRecipeAssistReply(reply);
    } catch (e) {
      setRecipeAssistErr(e instanceof Error ? e.message : String(e));
    } finally {
      setRecipeAssistBusy(false);
    }
  };

  const submitTunnel = async () => {
    if (!tunnelOpen) return;
    setTunnelBusy(true);
    setTunnelErr(null);
    try {
      const lp = tunnelLocalPort.trim();
      const rh = tunnelRemoteHost.trim();
      const rp = tunnelRemotePort.trim();

      let mapping = '';
      if (tunnelOpen.record.provider === 'k8s') {
        mapping = lp && rp ? `${lp}:${rp}` : rp ? rp : '';
      } else {
        mapping = lp && rp ? `${lp}:${rh || 'localhost'}:${rp}` : '';
      }

      if (!mapping) {
        throw new Error('Please specify valid ports.');
      }

      await startTunnel({
        ssh_user: sshUser.trim(),
        record: tunnelOpen.record,
        mapping,
      });
      setTunnelOpen(null);
      setTab('tunnels');
    } catch (e) {
      setTunnelErr(e instanceof Error ? e.message : String(e));
    } finally {
      setTunnelBusy(false);
    }
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
    setReplayListMeta(null);
    try {
      const resp = await fetchRecordingsList();
      setReplayItems(resp.items);
      setReplayListMeta({
        file_count: resp.file_count,
        total_bytes: resp.total_bytes,
        retention: resp.retention,
      });
      if (resp.items.length === 0) {
        setReplayErr('No files in record-dir yet.');
      }
    } catch (e) {
      setReplayErr(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Layout.Sider collapsible width={200} theme="dark">
        <div style={{ padding: '12px 16px', borderBottom: '1px solid #1d2535' }}>
          <Typography.Text strong style={{ color: '#e6e6e6', fontSize: 14 }}>
            honey
          </Typography.Text>
          {meta && (
            <Typography.Text style={{ color: '#666', fontSize: 11, marginLeft: 6 }}>
              v{meta.version}
            </Typography.Text>
          )}
        </div>
        {tokenMsg && (
          <Alert message={tokenMsg} type="warning" banner style={{ fontSize: 11 }} />
        )}
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[tab]}
          items={menuItems}
          onSelect={({ key }) => setTab(key as Tab)}
          style={{ borderRight: 0 }}
        />
      </Layout.Sider>

      <Layout>
        <Layout.Content style={{ padding: '16px 20px', overflowY: 'auto', minHeight: 0 }}>
          {tab === 'search' ? (
            <SearchTab
              records={records}
              selectedKeys={selectedKeys}
              onRecordsChange={setRecords}
              onSelectedKeysChange={setSelectedKeys}
              selectedProviders={selectedProviders}
              onSelectedProvidersChange={setSelectedProviders}
              selectedBackends={selectedBackends}
              onSelectedBackendsChange={setSelectedBackends}
              backends={backends}
              providerIds={providerIds}
              sshUser={sshUser}
              onSshUserChange={setSshUser}
              meta={meta}
              terminals={terminals}
              onOpenTunnel={(rec) => {
                setTunnelOpen({ record: rec });
                setTunnelLocalPort('');
                setTunnelRemotePort('');
                setTunnelRemoteHost('');
                setTunnelErr(null);
                setTunnelPorts([]);
                setTunnelPortsErr(null);
                if (rec.provider === 'k8s') {
                  setTunnelPortsLoading(false);
                  if (rec.meta?.ports) {
                    try {
                      const parsed = rec.meta.ports.split(',').map((p) => p.trim()).filter(Boolean);
                      setTunnelPorts(Array.isArray(parsed) ? parsed : []);
                    } catch {
                      // ignore
                    }
                  }
                } else {
                  setTunnelPortsLoading(true);
                  fetchHostPorts({ ssh_user: sshUser.trim(), record: rec })
                    .then((ports) => { setTunnelPorts(ports); })
                    .catch((e) => { setTunnelPortsErr(e instanceof Error ? e.message : String(e)); })
                    .finally(() => { setTunnelPortsLoading(false); });
                }
              }}
              onOpenReplay={openReplayModal}
              onOpenReplayAll={openReplayAllRecordings}
              onOpenTerminal={(cfg) => {
                setTerminals((prev) => [...prev, cfg]);
                setActiveTermId(cfg.id);
                setIsTerminalModalOpen(true);
              }}
            />
          ) : null}

          {tab === 'files' ? <FilesTab records={records} backends={backends} /> : null}
          {tab === 'backends' ? <BackendsTab backends={backends} error={backErr} /> : null}
          {tab === 'config' ? <ConfigTab /> : null}

          {tab === 'recipes' ? (
            <RecipesTab
              records={records}
              selectedRecords={selectedRecords}
              onSelectedRecordsChange={(hosts) => {
                const next: Record<string, boolean> = {};
                for (const h of hosts) next[recordKey(h)] = true;
                setSelectedKeys(next);
              }}
              onViewSource={(path, name) => void openRecipePreview(path, name)}
              onAiAssist={(path, name) => openRecipeAssist(path, name)}
              sessionRecordingAvailable={!!meta?.session_recording_available}
              terminalAssistAvailable={!!meta?.terminal_assist_available}
            />
          ) : null}

          {tab === 'tunnels' ? <TunnelsTab onNavigateToSearch={() => setTab('search')} /> : null}

          {tab === 'apps' ? (
            <AppsTab sshUser={sshUser} providers={selectedProviders} backends={selectedBackends} />
          ) : null}

          {tab === 'api-docs' ? <ApiDocsTab /> : null}
        </Layout.Content>
      </Layout>

      {/* Terminal modal — global, survives tab switches */}
      {terminals.length > 0 ? (
        <TerminalTabsModal
          isOpen={isTerminalModalOpen}
          terminals={terminals}
          activeTermId={activeTermId}
          sshUser={sshUser}
          recordSession={false}
          assistAvailable={!!meta?.terminal_assist_available}
          onSetActive={setActiveTermId}
          onCloseTerminal={(id) => {
            sessionStorage.removeItem(`honey_term_${id}`);
            setTerminals((prev) => {
              const next = prev.filter((t) => t.id !== id);
              if (activeTermId === id) setActiveTermId(next.length > 0 ? next[next.length - 1].id : null);
              if (next.length === 0) setIsTerminalModalOpen(false);
              return next;
            });
          }}
          onCloseModal={() => setIsTerminalModalOpen(false)}
        />
      ) : null}

      {/* Replay modal */}
      {replayRecord ? (
        replayItems.length > 0 ? (
          <SessionReplayModal
            record={replayRecord}
            recordings={replayItems}
            listStats={replayListMeta ? { file_count: replayListMeta.file_count, total_bytes: replayListMeta.total_bytes } : undefined}
            retention={replayListMeta?.retention}
            assistAvailable={!!meta?.terminal_assist_available}
            onRecordingsChange={() => void openReplayAllRecordings()}
            onClose={() => { setReplayRecord(null); setReplayListMeta(null); }}
          />
        ) : (
          <Modal
            open
            title="Session replay"
            onCancel={() => setReplayRecord(null)}
            footer={<Button onClick={() => setReplayRecord(null)}>Close</Button>}
            width="min(520px, 94vw)"
          >
            {replayErr ? <Alert type="error" message={replayErr} /> : <Spin tip="Loading recordings…" />}
          </Modal>
        )
      ) : null}

      {/* Tunnel creation modal */}
      <Modal
        open={!!tunnelOpen}
        title={tunnelOpen ? `Port Forward / Tunnel — ${tunnelOpen.record.name}` : 'Port Forward / Tunnel'}
        onCancel={() => setTunnelOpen(null)}
        footer={null}
        width="min(420px, 94vw)"
      >
        {tunnelOpen && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.65rem' }}>
            <Typography.Text type="secondary" style={{ fontSize: '0.85rem' }}>
              Configure a tunnel for <strong>{tunnelOpen.record.name}</strong>. The ports will be opened on the machine running the Honey server.
            </Typography.Text>
            <div>
              <Typography.Text style={{ fontSize: '0.85rem' }}>Local port (on server)</Typography.Text>
              <Input style={{ marginTop: 4 }} placeholder="e.g. 8080" value={tunnelLocalPort} onChange={(e) => setTunnelLocalPort(e.target.value)} />
            </div>
            {tunnelOpen.record.provider !== 'k8s' && (
              <div>
                <Typography.Text style={{ fontSize: '0.85rem' }}>Target remote host (optional, defaults to localhost)</Typography.Text>
                <Input style={{ marginTop: 4 }} placeholder="e.g. localhost" value={tunnelRemoteHost} onChange={(e) => setTunnelRemoteHost(e.target.value)} />
              </div>
            )}
            <div>
              <Typography.Text style={{ fontSize: '0.85rem' }}>Target remote port</Typography.Text>
              <Input style={{ marginTop: 4 }} placeholder="e.g. 80" value={tunnelRemotePort} onChange={(e) => setTunnelRemotePort(e.target.value)} />
              {tunnelPortsLoading && <Typography.Text type="secondary" style={{ fontSize: '0.8rem' }}>Detecting open ports…</Typography.Text>}
              {tunnelPortsErr && <Alert type="error" message={`Error detecting ports: ${tunnelPortsErr}`} style={{ marginTop: 4 }} />}
              {tunnelPorts.length > 0 && (
                <Space wrap style={{ marginTop: 4 }}>
                  {tunnelPorts.map((port) => (
                    <Button key={port} size="small" onClick={() => setTunnelRemotePort(port)} style={{ fontFamily: 'monospace' }}>{port}</Button>
                  ))}
                </Space>
              )}
              {!tunnelPortsLoading && !tunnelPortsErr && tunnelPorts.length === 0 && (
                <Typography.Text type="secondary" style={{ fontSize: '0.8rem' }}>No open ports detected.</Typography.Text>
              )}
            </div>
            {tunnelErr && <Alert type="error" message={tunnelErr} />}
            <Button
              type="primary"
              loading={tunnelBusy}
              disabled={!tunnelLocalPort.trim() || !tunnelRemotePort.trim()}
              onClick={() => void submitTunnel()}
            >
              Start Tunnel
            </Button>
          </div>
        )}
      </Modal>

      {/* Recipe preview modal */}
      <Modal
        open={!!recipePreview}
        title={recipePreview?.title}
        onCancel={() => setRecipePreview(null)}
        footer={<Button onClick={() => setRecipePreview(null)}>Close</Button>}
        width="min(720px, 96vw)"
        styles={{ body: { maxHeight: '80vh', overflow: 'auto', padding: 0 } }}
      >
        {recipePreview && (
          <Suspense fallback={<CodeLoadingFallback code={recipePreview.content} />}>
            <HighlightedCode
              className="recipe-preview-code"
              code={recipePreview.content}
              language={detectCodeLanguage(recipePreview.title)}
            />
          </Suspense>
        )}
      </Modal>

      {/* Recipe AI assist modal */}
      {recipeAssistOpen ? (
        <Modal
          open
          title={`AI explain: ${recipeAssistOpen.name}`}
          onCancel={() => closeRecipeAssist()}
          footer={<Button onClick={() => closeRecipeAssist()}>Close</Button>}
          width="min(640px, 96vw)"
          styles={{ body: { maxHeight: '80vh', overflow: 'auto', display: 'flex', flexDirection: 'column', gap: '0.55rem' } }}
        >
          <Typography.Text type="secondary" style={{ fontSize: '0.82rem' }}>
            Explanations are generated from the recipe file, optional dry-run against your{' '}
            <strong>selected hosts</strong> ({selectedRecords.length} selected), and your question. This is advisory—not
            a substitute for reviewing the CUE and dry-run output yourself before execute.
          </Typography.Text>
          {recipeAssistModelsLoading && <Spin size="small" />}
          {recipeAssistModelsErr && <Alert type="warning" message={recipeAssistModelsErr} />}
          {recipeAssistModels.length > 0 && (
            <div>
              <Typography.Text style={{ fontSize: '0.82rem' }}>Model</Typography.Text>
              <Select
                style={{ width: '100%', marginTop: 4 }}
                value={recipeAssistSelectedModel}
                onChange={setRecipeAssistSelectedModel}
                options={recipeAssistModels.map((id) => ({ value: id, label: id }))}
              />
            </div>
          )}
          <div>
            <Typography.Text style={{ fontSize: '0.82rem' }}>Question (optional)</Typography.Text>
            <Input.TextArea
              style={{ marginTop: 4 }}
              value={recipeAssistPrompt}
              onChange={(e) => setRecipeAssistPrompt(e.target.value)}
              placeholder="e.g. What does step 2 do on k8s pods?"
              rows={3}
            />
          </div>
          <Button
            type="primary"
            loading={recipeAssistBusy}
            disabled={recipeAssistModelsLoading || recipeAssistModels.length === 0 || !recipeAssistSelectedModel.trim()}
            onClick={() => void submitRecipeAssist()}
          >
            {recipeAssistBusy ? 'Thinking…' : 'Get explanation'}
          </Button>
          {recipeAssistErr && <Alert type="error" message={recipeAssistErr} />}
          {recipeAssistReply && (
            <div
              className="recipe-assist-reply"
              style={{ padding: '0.55rem', background: '#0f1115', border: '1px solid #2a3140', borderRadius: 6, maxHeight: '42vh', overflow: 'auto' }}
            >
              <Suspense fallback={<pre className="ai-markdown-suspense-fallback">{recipeAssistReply}</pre>}>
                <AiMarkdown content={recipeAssistReply} />
              </Suspense>
            </div>
          )}
        </Modal>
      ) : null}

      {/* Floating terminal button */}
      {terminals.length > 0 && !isTerminalModalOpen && (
        <Button
          type="primary"
          shape="round"
          style={{ position: 'fixed', bottom: 32, right: 32, zIndex: 40 }}
          onClick={() => setIsTerminalModalOpen(true)}
        >
          🖥️ Open Terminals ({terminals.length})
        </Button>
      )}
    </Layout>
  );
}
