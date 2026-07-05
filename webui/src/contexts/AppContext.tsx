import { createContext, useContext, useState, useEffect, type ReactNode } from 'react';
import { apiGet, getToken } from '../api/core';
import { useNavigation } from './NavigationContext';

export type BackendRow = { kind: string; name: string; hint: string };

interface AppContextType {
  tokenMsg: string;
  meta: {
    version: string;
    config_path: string;
    session_recording_available?: boolean;
    terminal_assist_available?: boolean;
    logs_command_allowed?: boolean;
  } | null;
  backends: BackendRow[];
  backErr: string | null;
}

const AppContext = createContext<AppContextType | null>(null);

export function AppProvider({ children }: { children: ReactNode }) {
  const [tokenMsg, setTokenMsg] = useState('');
  const [meta, setMeta] = useState<AppContextType['meta']>(null);
  const [backends, setBackends] = useState<BackendRow[]>([]);
  const [backErr, setBackErr] = useState<string | null>(null);
  
  const { tab } = useNavigation();

  useEffect(() => {
    if (!getToken()) {
      setTokenMsg('Add ?token=… to the URL (printed when you start honey web).');
    } else {
      setTokenMsg('');
    }
  }, []);

  useEffect(() => {
    async function loadMeta() {
      try {
        const r = await apiGet('/api/v1/meta');
        if (!r.ok) {
          setMeta(null);
          return;
        }
        const j = await r.json();
        setMeta(j);
      } catch {
        setMeta(null);
      }
    }
    void loadMeta();
  }, []);

  useEffect(() => {
    if (tab === 'backends' || tab === 'search' || tab === 'files') {
      async function loadBackends() {
        setBackErr(null);
        try {
          const r = await apiGet('/api/v1/backends');
          if (!r.ok) {
            const j = await r.json().catch(() => ({}));
            setBackErr(j.error || r.statusText);
            setBackends([]);
            return;
          }
          const j = await r.json();
          setBackends(j.backends || []);
        } catch (e) {
          setBackErr(e instanceof Error ? e.message : String(e));
          setBackends([]);
        }
      }
      void loadBackends();
    }
  }, [tab]);

  return (
    <AppContext.Provider value={{ tokenMsg, meta, backends, backErr }}>
      {children}
    </AppContext.Provider>
  );
}

export function useAppContext() {
  const ctx = useContext(AppContext);
  if (!ctx) throw new Error('useAppContext must be used within AppProvider');
  return ctx;
}
