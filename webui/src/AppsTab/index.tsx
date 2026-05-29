import React, { useState, useEffect, useCallback } from 'react';
import { fetchApps, fetchProxySessions, startProxySession, stopProxySession, fetchPostgresCatalog, runPostgresQuery, type AppConfig, type ProxySession, type PostgresCatalog } from '../api';
import { getToken } from '../api';
import CodeMirror from '@uiw/react-codemirror';
import { oneDark } from '@codemirror/theme-one-dark';
import { sql as sqlLang, PostgreSQL } from '@codemirror/lang-sql';
import { keymap, type EditorView } from '@codemirror/view';
import { autocompletion, type Completion, type CompletionContext } from '@codemirror/autocomplete';
import { Alert, Button, Card, Descriptions, Input, Modal, Pagination, Select, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import './apps-tab.css';

type CatalogTree = {
  schema: string;
  tables: Array<{ name: string; columns: string[] }>;
};

const sqlHistoryKey = (sessionId: string) => `honey_sql_history_${sessionId}`;

function loadSqlHistory(sessionId: string): string[] {
  try {
    const raw = localStorage.getItem(sqlHistoryKey(sessionId));
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((v): v is string => typeof v === 'string');
  } catch {
    return [];
  }
}

function saveSqlHistory(sessionId: string, history: string[]): void {
  try {
    localStorage.setItem(sqlHistoryKey(sessionId), JSON.stringify(history.slice(0, 20)));
  } catch {
    // ignore storage failures
  }
}

function buildCatalogCompletions(catalog: PostgresCatalog | null): Completion[] {
  if (!catalog) return [];
  const out: Completion[] = [];
  for (const db of catalog.databases || []) {
    out.push({ label: db, type: 'keyword', detail: 'database' });
  }
  for (const [schema, tables] of Object.entries(catalog.tables || {})) {
    out.push({ label: schema, type: 'namespace', detail: 'schema' });
    for (const table of tables) {
      out.push({ label: table, type: 'class', detail: `table (${schema})` });
      out.push({ label: `${schema}.${table}`, type: 'class', detail: 'qualified table' });
      const cols = (catalog.columns || {})[`${schema}.${table}`] || [];
      for (const col of cols) {
        out.push({ label: col, type: 'property', detail: `${schema}.${table}` });
      }
    }
  }
  return out;
}

function catalogCompletionSource(options: Completion[]) {
  return (context: CompletionContext) => {
    const word = context.matchBefore(/[\w.]+/);
    if (!word || (word.from === word.to && !context.explicit)) {
      return null;
    }
    const q = word.text.toLowerCase();
    const filtered = options.filter((o) => o.label.toLowerCase().includes(q)).slice(0, 200);
    return {
      from: word.from,
      options: filtered,
    };
  };
}

export function AppsTab({ sshUser, providers, backends }: { sshUser: string, providers: string[], backends: string[] }) {
  const [apps, setApps] = useState<{ [key: string]: AppConfig }>({});
  const [sessions, setSessions] = useState<ProxySession[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loadingApp, setLoadingApp] = useState<string | null>(null);
  const [stoppingSession, setStoppingSession] = useState<string | null>(null);
  const [sqlSession, setSqlSession] = useState<ProxySession | null>(null);
  const [sql, setSql] = useState<string>('select now();');
  const [catalog, setCatalog] = useState<PostgresCatalog | null>(null);
  const [queryRows, setQueryRows] = useState<Record<string, unknown>[]>([]);
  const [sqlError, setSqlError] = useState<string | null>(null);
  const [sqlLoading, setSqlLoading] = useState<boolean>(false);
  const [sqlHistory, setSqlHistory] = useState<string[]>([]);
  const [selectedHistory, setSelectedHistory] = useState<string>('');
  const [editorView, setEditorView] = useState<EditorView | null>(null);
  const [schemaFilter, setSchemaFilter] = useState<string>('');
  const [expandedSchemas, setExpandedSchemas] = useState<Record<string, boolean>>({});
  const [expandedTables, setExpandedTables] = useState<Record<string, boolean>>({});
  const [appFilter, setAppFilter] = useState('');
  const [appTypeFilter, setAppTypeFilter] = useState<string>('all');
  const [appPage, setAppPage] = useState(1);
  const APP_PAGE_SIZE = 12;
  const [sessionFilter, setSessionFilter] = useState('');

  const loadData = useCallback(async () => {
    try {
      const [appsData, sessionsData] = await Promise.all([
        fetchApps(),
        fetchProxySessions(),
      ]);
      setApps(appsData);
      setSessions(sessionsData);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    loadData();
    const interval = setInterval(loadData, 3000);
    return () => clearInterval(interval);
  }, [loadData]);

  const handleStart = async (appName: string) => {
    setLoadingApp(appName);
    setError(null);
    try {
      await startProxySession(appName, sshUser, providers, backends);
      await loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoadingApp(null);
    }
  };

  const handleStop = async (id: string) => {
    setStoppingSession(id);
    setError(null);
    try {
      await stopProxySession(id);
      await loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setStoppingSession(null);
    }
  };

  const openSqlModal = async (sess: ProxySession) => {
    setSqlSession(sess);
    setSqlError(null);
    setQueryRows([]);
    setSqlLoading(true);
    const hist = loadSqlHistory(sess.id);
    setSqlHistory(hist);
    setSelectedHistory('');
    try {
      const c = await fetchPostgresCatalog(sess.id);
      setCatalog(c);
    } catch (err) {
      setSqlError(err instanceof Error ? err.message : String(err));
      setCatalog(null);
    } finally {
      setSqlLoading(false);
    }
  };

  const runSql = async () => {
    if (!sqlSession) return;
    setSqlLoading(true);
    setSqlError(null);
    try {
      const r = await runPostgresQuery(sqlSession.id, sql);
      setQueryRows(r.rows || []);
      const normalized = sql.trim();
      if (normalized) {
        const next = [normalized, ...sqlHistory.filter((q) => q !== normalized)].slice(0, 20);
        setSqlHistory(next);
        saveSqlHistory(sqlSession.id, next);
      }
    } catch (err) {
      setSqlError(err instanceof Error ? err.message : String(err));
      setQueryRows([]);
    } finally {
      setSqlLoading(false);
    }
  };

  const runSelectedSql = async () => {
    if (!sqlSession || !editorView) return;
    const sel = editorView.state.selection.main;
    const text = editorView.state.sliceDoc(sel.from, sel.to).trim();
    if (!text) {
      await runSql();
      return;
    }
    setSqlLoading(true);
    setSqlError(null);
    try {
      const r = await runPostgresQuery(sqlSession.id, text);
      setQueryRows(r.rows || []);
      const next = [text, ...sqlHistory.filter((q) => q !== text)].slice(0, 20);
      setSqlHistory(next);
      saveSqlHistory(sqlSession.id, next);
    } catch (err) {
      setSqlError(err instanceof Error ? err.message : String(err));
      setQueryRows([]);
    } finally {
      setSqlLoading(false);
    }
  };

  const useTableSnippet = (schema: string, table: string) => {
    setSql(`select *\nfrom "${schema}"."${table}"\nlimit 100;`);
  };

  const toggleSchema = (schema: string) => {
    setExpandedSchemas((prev) => ({ ...prev, [schema]: !prev[schema] }));
  };

  const toggleTable = (schema: string, table: string) => {
    const k = `${schema}.${table}`;
    setExpandedTables((prev) => ({ ...prev, [k]: !prev[k] }));
  };

  const catalogTree = React.useMemo<CatalogTree[]>(() => {
    if (!catalog) return [];
    const out: CatalogTree[] = [];
    for (const [schema, tables] of Object.entries(catalog.tables || {})) {
      out.push({
        schema,
        tables: (tables || []).map((t) => ({
          name: t,
          columns: (catalog.columns || {})[`${schema}.${t}`] || [],
        })),
      });
    }
    return out.sort((a, b) => a.schema.localeCompare(b.schema));
  }, [catalog]);

  const filteredCatalogTree = React.useMemo<CatalogTree[]>(() => {
    const q = schemaFilter.trim().toLowerCase();
    if (!q) return catalogTree;
    const filtered: CatalogTree[] = [];
    for (const s of catalogTree) {
      const schemaMatch = s.schema.toLowerCase().includes(q);
      const tables = s.tables.filter((t) => {
        if (schemaMatch) return true;
        if (t.name.toLowerCase().includes(q)) return true;
        return t.columns.some((c) => c.toLowerCase().includes(q));
      });
      if (schemaMatch || tables.length > 0) {
        filtered.push({ schema: s.schema, tables });
      }
    }
    return filtered;
  }, [catalogTree, schemaFilter]);

  const appList = React.useMemo(() => Object.values(apps), [apps]);

  const filteredApps = React.useMemo(() => {
    const q = appFilter.trim().toLowerCase();
    return appList.filter((app) => {
      if (appTypeFilter !== 'all' && app.type !== appTypeFilter) return false;
      if (!q) return true;
      return (
        app.name.toLowerCase().includes(q) ||
        (app.mode || '').toLowerCase().includes(q) ||
        (app.backend || '').toLowerCase().includes(q) ||
        (app.provider || '').toLowerCase().includes(q)
      );
    });
  }, [appList, appFilter, appTypeFilter]);

  const pagedApps = React.useMemo(() => {
    const start = (appPage - 1) * APP_PAGE_SIZE;
    return filteredApps.slice(start, start + APP_PAGE_SIZE);
  }, [filteredApps, appPage]);

  useEffect(() => { setAppPage(1); }, [appFilter, appTypeFilter]);

  const filteredSessions = React.useMemo(() => {
    const q = sessionFilter.trim().toLowerCase();
    if (!q) return sessions;
    return sessions.filter((s) =>
      s.app.name.toLowerCase().includes(q) ||
      s.app.type.toLowerCase().includes(q) ||
      (s.app.mode || '').toLowerCase().includes(q)
    );
  }, [sessions, sessionFilter]);

  const sessionColumns: ColumnsType<ProxySession> = [
    { title: 'App', dataIndex: ['app', 'name'], key: 'app_name' },
    { title: 'Type', dataIndex: ['app', 'type'], key: 'app_type' },
    {
      title: 'Local Address',
      key: 'local_addr',
      render: (_: unknown, sess: ProxySession) => sess.app.type === 'http' ? (
        <a href={`${window.location.protocol}//${sess.app.name}.localhost${window.location.port ? ':' + window.location.port : ''}/?token=${getToken()}`} target="_blank" rel="noreferrer" title={`Internal Proxy -> ${sess.app.upstream}`}>
          Open Web App ↗
        </a>
      ) : (
        <Space>
          <span>{sess.local_addr || 'Web Proxy Only'}</span>
          {sess.app.type === 'tcp' && (sess.app.mode || '').toLowerCase() === 'postgres' && (
            <Button size="small" onClick={() => openSqlModal(sess)}>SQL</Button>
          )}
        </Space>
      ),
    },
    {
      title: 'Upstream',
      key: 'upstream',
      render: (_: unknown, sess: ProxySession) => sess.app.upstream === '[encrypted]' ? '(encrypted)' : sess.app.upstream,
    },
    {
      title: 'Started',
      key: 'started',
      render: (_: unknown, sess: ProxySession) => new Date(sess.started_at).toLocaleTimeString(),
    },
    {
      title: 'Actions',
      key: 'actions',
      render: (_: unknown, sess: ProxySession) => (
        <Button loading={stoppingSession === sess.id} onClick={() => handleStop(sess.id)}>Stop</Button>
      ),
    },
  ];

  const sqlResultColumns: ColumnsType<Record<string, unknown>> = queryRows.length > 0
    ? Object.keys(queryRows[0]).map((k) => ({
        title: k,
        dataIndex: k,
        key: k,
        ellipsis: true,
        render: (val: unknown) => String(val ?? ''),
      }))
    : [];

  return (
    <div className="apps-tab">
      {error && <Alert type="error" title={error} style={{ marginBottom: 12 }} />}

      <section className="apps-section">
        <Typography.Title level={5}>Configured Apps</Typography.Title>
        <Space wrap style={{ marginBottom: 12 }}>
          <Input
            placeholder="Filter by name, mode, backend…"
            value={appFilter}
            onChange={(e) => setAppFilter(e.target.value)}
            allowClear
            style={{ width: 240 }}
          />
          <Select
            value={appTypeFilter}
            onChange={setAppTypeFilter}
            style={{ width: 110 }}
            options={[
              { value: 'all', label: 'All types' },
              { value: 'http', label: 'HTTP' },
              { value: 'tcp', label: 'TCP' },
            ]}
          />
        </Space>
        {appList.length === 0 ? (
          <Typography.Text type="secondary">No apps configured in honey.yaml.</Typography.Text>
        ) : filteredApps.length === 0 ? (
          <Typography.Text type="secondary">No apps match the current filter.</Typography.Text>
        ) : (
          <>
            <div className="apps-grid">
              {pagedApps.map((app) => {
                const activeSession = sessions.find((s) => s.app.name === app.name);
                return (
                  <Card key={app.name} size="small" title={app.name} className="app-card">
                    <Descriptions
                      column={1}
                      size="small"
                      colon={false}
                      labelStyle={{ width: 90, color: 'var(--ant-color-text-secondary)', fontSize: '0.82rem' }}
                      contentStyle={{ fontSize: '0.82rem' }}
                      items={[
                        {
                          key: 'type',
                          label: 'Type',
                          children: (
                            <Space size={4}>
                              {app.type}
                              {app.mode && <Tag color="blue">{app.mode}</Tag>}
                            </Space>
                          ),
                        },
                        ...(app.backend ? [{ key: 'backend', label: 'Backend', children: app.backend }] : []),
                        ...(app.provider ? [{ key: 'provider', label: 'Provider', children: app.provider }] : []),
                        {
                          key: 'target',
                          label: app.target_regex ? 'Target Regex' : 'Target',
                          children: app.target_regex || app.target || 'local',
                        },
                        {
                          key: 'upstream',
                          label: 'Upstream',
                          children:
                            app.upstream === '[encrypted]' ? (
                              <Typography.Text type="secondary">(encrypted)</Typography.Text>
                            ) : (
                              <Typography.Text ellipsis={{ tooltip: app.upstream }}>
                                {app.upstream}
                              </Typography.Text>
                            ),
                        },
                        ...(app.local_port > 0
                          ? [{ key: 'port', label: 'Local Port', children: app.local_port }]
                          : []),
                      ]}
                    />
                    <Space style={{ marginTop: 8, width: '100%' }}>
                      {activeSession ? (
                        <>
                          {app.type === 'http' && (
                            <Button
                              type="primary"
                              href={`${window.location.protocol}//${app.name}.localhost${window.location.port ? ':' + window.location.port : ''}/?token=${getToken()}`}
                              target="_blank"
                              rel="noreferrer"
                              style={{ flex: 1 }}
                            >
                              Open App ↗
                            </Button>
                          )}
                          <Button
                            style={{ flex: 1 }}
                            loading={stoppingSession === activeSession.id}
                            onClick={() => handleStop(activeSession.id)}
                          >
                            Stop
                          </Button>
                        </>
                      ) : (
                        <Button
                          type="primary"
                          style={{ width: '100%' }}
                          loading={loadingApp === app.name}
                          onClick={() => handleStart(app.name)}
                        >
                          Start {app.type.toUpperCase()}
                        </Button>
                      )}
                    </Space>
                  </Card>
                );
              })}
            </div>
            {filteredApps.length > APP_PAGE_SIZE && (
              <div style={{ marginTop: 12, display: 'flex', justifyContent: 'flex-end' }}>
                <Pagination
                  current={appPage}
                  pageSize={APP_PAGE_SIZE}
                  total={filteredApps.length}
                  onChange={setAppPage}
                  showTotal={(total) => `${total} apps`}
                  size="small"
                />
              </div>
            )}
          </>
        )}
      </section>

      <section className="sessions-section" style={{ marginTop: '2rem' }}>
        <Typography.Title level={5}>Active Proxy Sessions</Typography.Title>
        {sessions.length === 0 ? (
          <Typography.Text type="secondary">No active proxy sessions.</Typography.Text>
        ) : (
          <>
            <Space style={{ marginBottom: 8 }}>
              <Input
                placeholder="Filter sessions…"
                value={sessionFilter}
                onChange={(e) => setSessionFilter(e.target.value)}
                allowClear
                style={{ width: 220 }}
              />
            </Space>
            {filteredSessions.length === 0 ? (
              <Typography.Text type="secondary">No sessions match the filter.</Typography.Text>
            ) : (
              <Table<ProxySession>
                dataSource={filteredSessions}
                columns={sessionColumns}
                rowKey="id"
                size="small"
                pagination={{ pageSize: 10, showSizeChanger: true, showTotal: (n) => `${n} sessions` }}
              />
            )}
          </>
        )}
      </section>

      <Modal
        open={!!sqlSession}
        title={sqlSession ? `SQL Editor — ${sqlSession.app.name}` : 'SQL Editor'}
        onCancel={() => setSqlSession(null)}
        footer={null}
        width="min(1100px, 96vw)"
        styles={{ body: { padding: 0 } }}
      >
        <div className="sql-layout">
          <aside className="sql-sidebar">
            <Typography.Title level={5} style={{ margin: '0 0 8px' }}>Postgres Catalog</Typography.Title>
            <div className="sql-schema-search-wrap">
              <Input.Search
                placeholder="Search schema..."
                aria-label="Search schema"
                value={schemaFilter}
                onChange={(e) => setSchemaFilter(e.target.value)}
                allowClear
              />
            </div>
            {catalog ? (
              <>
                <div style={{ marginBottom: '0.75rem' }}>
                  <strong>Databases</strong>
                  <div className="sql-chip-list">
                    {catalog.databases.map((d) => <span className="sql-chip" key={d}>{d}</span>)}
                  </div>
                </div>
                <div>
                  <strong>Schemas & Tables</strong>
                  {filteredCatalogTree.length === 0 ? (
                    <p style={{ opacity: 0.75 }}>No schema objects match "{schemaFilter}".</p>
                  ) : (
                    <div className="sql-schema-tree">
                      {filteredCatalogTree.map(({ schema, tables }) => {
                        const schemaOpen = expandedSchemas[schema] ?? true;
                        return (
                          <div key={schema} className="sql-schema-group">
                            <button className="sql-tree-toggle" onClick={() => toggleSchema(schema)}>
                              <span>{schemaOpen ? '▾' : '▸'}</span>
                              <strong>{schema}</strong>
                              <span className="sql-muted">{tables.length} tables</span>
                            </button>
                            {schemaOpen && (
                              <div className="sql-table-list">
                                {tables.map((t) => {
                                  const tk = `${schema}.${t.name}`;
                                  const tableOpen = expandedTables[tk] ?? false;
                                  return (
                                    <div key={tk} className="sql-table-item">
                                      <div className="sql-table-row">
                                        <button className="sql-tree-toggle sql-table-name" onClick={() => toggleTable(schema, t.name)}>
                                          <span>{tableOpen ? '▾' : '▸'}</span>
                                          <span>{t.name}</span>
                                        </button>
                                        <div className="sql-table-actions">
                                          <button onClick={() => useTableSnippet(schema, t.name)} title="Insert SELECT snippet">↦</button>
                                        </div>
                                      </div>
                                      {tableOpen && t.columns.length > 0 && (
                                        <div className="sql-column-list">
                                          {t.columns.map((c) => (
                                            <div key={`${tk}.${c}`} className="sql-column-item">{c}</div>
                                          ))}
                                        </div>
                                      )}
                                    </div>
                                  );
                                })}
                              </div>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>
              </>
            ) : (
              <p style={{ opacity: 0.75 }}>{sqlLoading ? 'Loading catalog...' : 'No catalog loaded.'}</p>
            )}
          </aside>
          <section className="sql-content">
            <div className="sql-editor-wrap">
              {(() => {
                const completionOptions = buildCatalogCompletions(catalog);
                const completionSource = catalogCompletionSource(completionOptions);
                return (
                  <CodeMirror
                    value={sql}
                    height="180px"
                    theme={oneDark}
                    onChange={(value) => setSql(value)}
                    onCreateEditor={(view) => setEditorView(view)}
                    extensions={[
                      sqlLang({ dialect: PostgreSQL }),
                      autocompletion({ override: [completionSource] }),
                      keymap.of([
                        {
                          key: 'Mod-Enter',
                          run: () => {
                            void runSelectedSql();
                            return true;
                          },
                        },
                      ]),
                    ]}
                    basicSetup={{
                      lineNumbers: true,
                      foldGutter: true,
                      highlightActiveLine: true,
                      bracketMatching: true,
                    }}
                  />
                );
              })()}
            </div>
            <div className="sql-controls">
              <Button type="primary" loading={sqlLoading} onClick={runSql}>Run Query</Button>
              <Button loading={sqlLoading} onClick={runSelectedSql}>Run Selection (Cmd/Ctrl+Enter)</Button>
              <Button onClick={() => setSqlSession(null)}>Close</Button>
            </div>
            <div className="sql-history-wrap">
              <label style={{ display: 'block', marginBottom: 4, fontSize: 12, opacity: 0.8 }}>History</label>
              <Select
                value={selectedHistory || undefined}
                onChange={(v) => { setSelectedHistory(v); setSql(v); }}
                placeholder="Select previous query..."
                style={{ width: '100%' }}
                options={sqlHistory.map((h, i) => ({
                  value: h,
                  label: h.length > 120 ? h.slice(0, 120) + '...' : h,
                  key: `${i}:${h.slice(0, 20)}`,
                }))}
                allowClear
              />
            </div>
            {sqlError && <Alert type="error" title={sqlError} style={{ marginTop: 8 }} />}
            <div className="sql-results-wrap">
              {queryRows.length === 0 ? (
                <p style={{ opacity: 0.75 }}>No rows yet.</p>
              ) : (
                <div className="sql-table-scroll">
                  <Table<Record<string, unknown>>
                    dataSource={queryRows.map((row, i) => ({ ...row, __key: i }))}
                    columns={sqlResultColumns}
                    rowKey="__key"
                    size="small"
                    pagination={false}
                    scroll={{ x: 'max-content' }}
                  />
                </div>
              )}
            </div>
          </section>
        </div>
      </Modal>
    </div>
  );
}
