import React, { useState, useEffect, useCallback } from 'react';
import { fetchApps, fetchProxySessions, startProxySession, stopProxySession, type AppConfig, type ProxySession } from '../api';
import { getToken } from '../api';
import './apps-tab.css';

export function AppsTab({ sshUser, providers, backends }: { sshUser: string, providers: string[], backends: string[] }) {
  const [apps, setApps] = useState<{ [key: string]: AppConfig }>({});
  const [sessions, setSessions] = useState<ProxySession[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loadingApp, setLoadingApp] = useState<string | null>(null);
  const [stoppingSession, setStoppingSession] = useState<string | null>(null);

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
                  <div><strong>Upstream:</strong> {app.upstream}</div>
                  {app.local_port > 0 && <div><strong>Local Port:</strong> {app.local_port}</div>}
                </div>
                <div style={{ display: 'flex', gap: '0.5rem', marginTop: '0.5rem' }}>
                  {activeSession ? (
                    <>
                      {app.type === 'http' && (
                        <a 
                          href={`${window.location.protocol}//${app.name}.${window.location.hostname === '127.0.0.1' ? 'localhost' : window.location.hostname}${window.location.port ? ':' + window.location.port : ''}/?token=${getToken()}`} 
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
                      <a href={`${window.location.protocol}//${sess.app.name}.${window.location.hostname === '127.0.0.1' ? 'localhost' : window.location.hostname}${window.location.port ? ':' + window.location.port : ''}/?token=${getToken()}`} target="_blank" rel="noreferrer" title={`Internal Proxy -> ${sess.app.upstream}`}>
                        Open Web App ↗
                      </a>
                    ) : (
                      sess.local_addr || 'Web Proxy Only'
                    )}
                  </td>
                  <td>{sess.app.upstream}</td>
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
    </div>
  );
}
