import React, { useState, useEffect, useCallback } from 'react';
import { fetchApps, fetchProxySessions, startProxySession, stopProxySession, fetchPostgresCatalog, runPostgresQuery, type AppConfig, type ProxySession, type PostgresCatalog } from '../api';
import { getToken } from '../api';
import CodeMirror from '@uiw/react-codemirror';
import { oneDark } from '@codemirror/theme-one-dark';
import { sql as sqlLang, PostgreSQL } from '@codemirror/lang-sql';
import { keymap, type EditorView } from '@codemirror/view';
import { autocompletion, type Completion, type CompletionContext } from '@codemirror/autocomplete';
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

  return (
    <div className="apps-tab">
      {error && <p style={{ color: '#f66' }}>{error}</p>}
      
      <section className="apps-section">
        <h3>Configured Apps</h3>
        {Object.keys(apps).length === 0 ? (
          <p style={{ opacity: 0.8 }}>No apps configured in honey.yaml.</p>
        ) : (
          <div className="apps-grid">
            {Object.values(apps).map((app) => {
              const activeSession = sessions.find(s => s.app.name === app.name);
              return (
              <div key={app.name} className="app-card">
                <h4>{app.name}</h4>
                <div className="app-details">
                  <div><strong>Type:</strong> {app.type}</div>
                  {app.backend && <div><strong>Backend:</strong> {app.backend}</div>}
                  {app.provider && <div><strong>Provider:</strong> {app.provider}</div>}
                  {app.target_regex ? (
                    <div><strong>Target Regex:</strong> {app.target_regex}</div>
                  ) : (
                    <div><strong>Target:</strong> {app.target || 'local'}</div>
                  )}
                  <div>
                    <strong>Upstream:</strong>{' '}
                    {app.upstream === '[encrypted]' ? (
                      <span style={{ opacity: 0.8 }}>(encrypted)</span>
                    ) : (
                      <span
                        title={app.upstream}
                        style={{
                          display: 'inline-block',
                          maxWidth: '100%',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                          verticalAlign: 'bottom',
                        }}
                      >
                        {app.upstream}
                      </span>
                    )}
                  </div>
                  {app.local_port > 0 && <div><strong>Local Port:</strong> {app.local_port}</div>}
                </div>
                <div style={{ display: 'flex', gap: '0.5rem', marginTop: '0.5rem' }}>
                  {activeSession ? (
                    <>
                      {app.type === 'http' && (
                        <a 
                          href={`${window.location.protocol}//${app.name}.localhost${window.location.port ? ':' + window.location.port : ''}/?token=${getToken()}`} 
                          target="_blank" 
                          rel="noreferrer" 
                          className="button primary"
                          style={{ textDecoration: 'none', textAlign: 'center', flex: 1, padding: '0.4rem', border: '1px solid transparent', borderRadius: '4px', background: '#2563eb', color: '#fff', fontSize: '0.85rem' }}
                        >
                          Open App ↗
                        </a>
                      )}
                      <button
                        style={{ flex: 1 }}
                        disabled={stoppingSession === activeSession.id}
                        onClick={() => handleStop(activeSession.id)}
                      >
                        {stoppingSession === activeSession.id ? 'Stopping...' : 'Stop'}
                      </button>
                    </>
                  ) : (
                    <button
                      className="primary"
                      style={{ width: '100%' }}
                      disabled={loadingApp === app.name}
                      onClick={() => handleStart(app.name)}
                    >
                      {loadingApp === app.name ? 'Starting...' : `Start ${app.type.toUpperCase()}`}
                    </button>
                  )}
                </div>
              </div>
            )})}
          </div>
        )}
      </section>

      <section className="sessions-section" style={{ marginTop: '2rem' }}>
        <h3>Active Proxy Sessions</h3>
        {sessions.length === 0 ? (
          <p style={{ opacity: 0.8 }}>No active proxy sessions.</p>
        ) : (
          <table className="sessions-table">
            <thead>
              <tr>
                <th>App</th>
                <th>Type</th>
                <th>Local Address</th>
                <th>Upstream</th>
                <th>Started</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((sess) => (
                <tr key={sess.id}>
                  <td>{sess.app.name}</td>
                  <td>{sess.app.type}</td>
                  <td>
                    {sess.app.type === 'http' ? (
                      <a href={`${window.location.protocol}//${sess.app.name}.localhost${window.location.port ? ':' + window.location.port : ''}/?token=${getToken()}`} target="_blank" rel="noreferrer" title={`Internal Proxy -> ${sess.app.upstream}`}>
                        Open Web App ↗
                      </a>
                    ) : (
                      <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
                        <span>{sess.local_addr || 'Web Proxy Only'}</span>
                        {sess.app.type === 'tcp' && (sess.app.mode || '').toLowerCase() === 'postgres' && (
                          <button onClick={() => openSqlModal(sess)}>SQL</button>
                        )}
                      </div>
                    )}
                  </td>
                  <td
                    title={sess.app.upstream === '[encrypted]' ? '' : sess.app.upstream}
                    style={{ maxWidth: 360, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                  >
                    {sess.app.upstream === '[encrypted]' ? '(encrypted)' : sess.app.upstream}
                  </td>
                  <td>{new Date(sess.started_at).toLocaleTimeString()}</td>
                  <td>
                    <button
                      disabled={stoppingSession === sess.id}
                      onClick={() => handleStop(sess.id)}
                    >
                      {stoppingSession === sess.id ? 'Stopping...' : 'Stop'}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {sqlSession && (
        <div className="modal-backdrop" onClick={() => setSqlSession(null)}>
          <div className="modal sql-modal" role="dialog" aria-labelledby="sql-modal-title" onClick={(e) => e.stopPropagation()}>
            <div className="sql-layout">
            <aside className="sql-sidebar">
              <h3 id="sql-modal-title" style={{ marginTop: 0 }}>Postgres Catalog</h3>
              <div className="sql-schema-search-wrap">
                <input
                  className="sql-schema-search"
                  type="text"
                  placeholder="Search schema..."
                  aria-label="Search schema"
                  value={schemaFilter}
                  onChange={(e) => setSchemaFilter(e.target.value)}
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
              <h3 style={{ marginTop: 0 }}>SQL Editor - {sqlSession.app.name}</h3>
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
                <button className="primary" onClick={runSql} disabled={sqlLoading}>{sqlLoading ? 'Running...' : 'Run Query'}</button>
                <button onClick={runSelectedSql} disabled={sqlLoading}>Run Selection (Cmd/Ctrl+Enter)</button>
                <button onClick={() => setSqlSession(null)}>Close</button>
              </div>
              <div className="sql-history-wrap">
                <label style={{ display: 'block', marginBottom: 4, fontSize: 12, opacity: 0.8 }}>History</label>
                <select
                  value={selectedHistory}
                  onChange={(e) => {
                    const v = e.target.value;
                    setSelectedHistory(v);
                    if (v) setSql(v);
                  }}
                  style={{ width: '100%' }}
                >
                  <option value="">Select previous query...</option>
                  {sqlHistory.map((h, idx) => (
                    <option key={`${idx}:${h.slice(0, 20)}`} value={h}>
                      {h.length > 120 ? `${h.slice(0, 120)}...` : h}
                    </option>
                  ))}
                </select>
              </div>
              {sqlError && <p style={{ color: '#f66' }}>{sqlError}</p>}
              <div className="sql-results-wrap">
                {queryRows.length === 0 ? (
                  <p style={{ opacity: 0.75 }}>No rows yet.</p>
                ) : (
                  <div className="sql-table-scroll">
                    <table className="sessions-table sql-results-table">
                      <thead>
                        <tr>{Object.keys(queryRows[0] || {}).map((k) => <th key={k}>{k}</th>)}</tr>
                      </thead>
                      <tbody>
                        {queryRows.map((row, i) => (
                          <tr key={i}>{Object.keys(queryRows[0] || {}).map((k) => <td key={k} title={String(row[k] ?? '')}>{String(row[k] ?? '')}</td>)}</tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            </section>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
