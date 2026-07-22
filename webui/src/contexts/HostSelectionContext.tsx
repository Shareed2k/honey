import { createContext, useContext, useState, useEffect, useMemo, type ReactNode } from 'react';
import type { HostRecord } from '../HostPicker';
import { recordKey } from '../HostPicker';
import { apiGet } from '../api/core';

interface HostSelectionContextType {
  selectedProviders: string[];
  setSelectedProviders: (providers: string[]) => void;
  selectedBackends: string[];
  setSelectedBackends: (backends: string[]) => void;
  providerIds: string[];
  records: HostRecord[];
  setRecords: React.Dispatch<React.SetStateAction<HostRecord[]>>;
  sshUser: string;
  setSshUser: (user: string) => void;
  selectedKeys: Record<string, boolean>;
  // Widened to the real dispatch type: this is the useState setter, so it
  // genuinely accepts a functional updater `(prev) => next` in addition to a
  // plain value. Callers that only ever passed a plain map (SearchTab,
  // ReplayContext, RecipesTab) remain valid — `next` is still assignable to
  // `SetStateAction<Record<string, boolean>>`. RecordsPanel relies on the
  // functional form to avoid a stale-closure bug when antd's Table calls
  // onSelectAll's per-row callback synchronously in a loop (see
  // RecordsPanel.tsx onToggleRow).
  setSelectedKeys: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  selectedRecords: HostRecord[];
}

const HostSelectionContext = createContext<HostSelectionContextType | null>(null);

function updateUrlParam(key: string, value: string | null) {
  const params = new URLSearchParams(window.location.search);
  if (value === null) params.delete(key);
  else params.set(key, value);
  window.history.replaceState(null, '', `?${params.toString()}`);
}

export function HostSelectionProvider({ children }: { children: ReactNode }) {
  const [selectedProviders, setSelectedProviders] = useState<string[]>(() => {
    const val = new URLSearchParams(window.location.search).get('selectedProviders');
    return val ? val.split(',') : [];
  });
  
  const [selectedBackends, setSelectedBackends] = useState<string[]>(() => {
    const val = new URLSearchParams(window.location.search).get('selectedBackends');
    return val ? val.split(',') : [];
  });

  const [providerIds, setProviderIds] = useState<string[]>([]);
  const [records, setRecords] = useState<HostRecord[]>([]);
  
  const [sshUser, setSshUser] = useState(() => new URLSearchParams(window.location.search).get('sshUser') || '');
  
  const [selectedKeys, setSelectedKeys] = useState<Record<string, boolean>>(() => {
    const val = new URLSearchParams(window.location.search).get('selectedKeys');
    if (!val) return {};
    return val.split(',').reduce((acc, key) => ({ ...acc, [key]: true }), {});
  });

  useEffect(() => {
    updateUrlParam('selectedProviders', selectedProviders.length > 0 ? selectedProviders.join(',') : null);
  }, [selectedProviders]);

  useEffect(() => {
    updateUrlParam('selectedBackends', selectedBackends.length > 0 ? selectedBackends.join(',') : null);
  }, [selectedBackends]);

  useEffect(() => {
    updateUrlParam('sshUser', sshUser || null);
  }, [sshUser]);

  useEffect(() => {
    const keys = Object.keys(selectedKeys).filter((k) => selectedKeys[k]);
    updateUrlParam('selectedKeys', keys.length > 0 ? keys.join(',') : null);
  }, [selectedKeys]);

  useEffect(() => {
    async function loadProviders() {
      try {
        const r = await apiGet('/api/v1/providers');
        if (!r.ok) return;
        const j = (await r.json()) as { providers?: string[] };
        setProviderIds(j.providers || []);
      } catch {
        setProviderIds([]);
      }
    }
    void loadProviders();
  }, []);

  const selectedRecords = useMemo(
    () => records.filter((r) => selectedKeys[recordKey(r)]),
    [records, selectedKeys],
  );

  return (
    <HostSelectionContext.Provider value={{
      selectedProviders, setSelectedProviders,
      selectedBackends, setSelectedBackends,
      providerIds,
      records, setRecords,
      sshUser, setSshUser,
      selectedKeys, setSelectedKeys,
      selectedRecords,
    }}>
      {children}
    </HostSelectionContext.Provider>
  );
}

export function useHostSelection() {
  const ctx = useContext(HostSelectionContext);
  if (!ctx) throw new Error('useHostSelection must be used within HostSelectionProvider');
  return ctx;
}
