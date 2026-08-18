import { createContext, useContext, useState, useEffect, useRef, useCallback, type ReactNode } from 'react';
import { TerminalTabsModal, type TerminalSessionConfig, type PveConsoleMode, type TrueNASConsoleMode } from '../TerminalModal';
import { type InterceptOptions, fetchInterceptSessions } from '../api/intercept';
import { useHostSelection } from './HostSelectionContext';
import { useAppContext } from './AppContext';
import { recordKey } from '../HostPicker';

interface TerminalContextType {
  terminals: TerminalSessionConfig[];
  activeTermId: string | null;
  isTerminalModalOpen: boolean;
  handleOpenTerminal: (cfg: TerminalSessionConfig) => void;
  setIsTerminalModalOpen: (open: boolean) => void;
  // closeTerminal removes a tab (and clears its saved record). The sessions-list
  // Stop button uses it to drop the tab the instant a session is stopped,
  // instead of waiting for the reconcile poll.
  closeTerminal: (id: string) => void;
}

const TerminalContext = createContext<TerminalContextType | null>(null);

function updateUrlParam(key: string, value: string | null) {
  const params = new URLSearchParams(window.location.search);
  if (value === null) params.delete(key);
  else params.set(key, value);
  window.history.replaceState(null, '', `?${params.toString()}`);
}

// termRecordKey is the stable per-pod key a terminal tab carries: the `_key`
// restored from the URL, or the record's computed key for a freshly opened tab.
// It matches InterceptSession.record_key so open tabs can be reconciled against
// the server's active-session list.
function termRecordKey(t: TerminalSessionConfig): string {
  return (t.record as { _key?: string })._key || recordKey(t.record);
}

export function TerminalProvider({ children }: { children: ReactNode }) {
  const { sshUser } = useHostSelection();
  const { meta } = useAppContext();

  const [terminals, setTerminals] = useState<TerminalSessionConfig[]>(() => {
    const val = new URLSearchParams(window.location.search).get('terminals');
    if (!val) return [];
    try {
      return val.split(',').map(part => {
        const [id, key, pve, truenasConsole, interceptRaw] = part.split('|');
        const sessionRec = sessionStorage.getItem(`honey_term_${id}`);
        const record = sessionRec ? JSON.parse(sessionRec) : { _key: key, provider: 'loading', name: 'loading', primary_ip: '' };
        let intercept: InterceptOptions | undefined;
        if (interceptRaw) {
          try {
            intercept = JSON.parse(decodeURIComponent(interceptRaw)) as InterceptOptions;
          } catch {
            intercept = undefined;
          }
        }
        return {
          id: id || crypto.randomUUID(),
          record,
          pve: (pve as PveConsoleMode) || 'serial',
          truenasConsole: (truenasConsole as TrueNASConsoleMode) || 'ssh',
          intercept,
        };
      });
    } catch {
      return [];
    }
  });

  const [activeTermId, setActiveTermId] = useState<string | null>(() => new URLSearchParams(window.location.search).get('activeTermId'));
  const [isTerminalModalOpen, setIsTerminalModalOpen] = useState(() => new URLSearchParams(window.location.search).get('isTerminalModalOpen') === 'true');

  useEffect(() => {
    if (terminals.length > 0) {
      const joined = terminals.map(t => {
        const rKey = termRecordKey(t);
        const parts = [t.id, rKey, t.pve || 'serial', t.truenasConsole || 'ssh'];
        if (t.intercept) {
          parts.push(encodeURIComponent(JSON.stringify(t.intercept)));
        }
        return parts.join('|');
      }).join(',');
      updateUrlParam('terminals', joined);
    } else {
      updateUrlParam('terminals', null);
    }
  }, [terminals]);

  useEffect(() => {
    updateUrlParam('activeTermId', activeTermId || null);
  }, [activeTermId]);

  useEffect(() => {
    updateUrlParam('isTerminalModalOpen', isTerminalModalOpen ? 'true' : null);
  }, [isTerminalModalOpen]);

  const closeTerminal = useCallback((id: string) => {
    sessionStorage.removeItem(`honey_term_${id}`);
    setTerminals((prev) => {
      const next = prev.filter((t) => t.id !== id);
      setActiveTermId((cur) => (cur === id ? (next.length > 0 ? next[next.length - 1].id : null) : cur));
      if (next.length === 0) setIsTerminalModalOpen(false);
      return next;
    });
  }, []);

  // Reconcile open intercept tabs against the server's active-session list.
  // An intercept session survives a browser refresh (the tmux pane keeps
  // running), so a closed WebSocket alone must NOT close its tab. The list is
  // the truth: once a tab's session has been seen active and then disappears
  // (Stop, the shell exiting, or the pane dying), the session is gone for good
  // — drop the now-dead tab. A tab whose session has never appeared yet (just
  // opened, poll still stale) is left alone via the seen-key gate.
  const seenKeysRef = useRef<Set<string>>(new Set());
  const hasInterceptTab = terminals.some((t) => t.intercept);
  useEffect(() => {
    if (!hasInterceptTab) {
      seenKeysRef.current.clear();
      return;
    }
    let cancelled = false;
    const reconcile = async () => {
      const sessions = await fetchInterceptSessions().catch(() => null);
      if (cancelled || sessions === null) return;
      const active = new Set(sessions.map((s) => s.record_key).filter((k): k is string => !!k));
      active.forEach((k) => seenKeysRef.current.add(k));
      setTerminals((prev) => {
        const survivors = prev.filter((t) => {
          if (!t.intercept) return true;
          const key = termRecordKey(t);
          if (seenKeysRef.current.has(key) && !active.has(key)) {
            sessionStorage.removeItem(`honey_term_${t.id}`);
            seenKeysRef.current.delete(key);
            return false;
          }
          return true;
        });
        if (survivors.length === prev.length) return prev;
        setActiveTermId((cur) => (survivors.some((t) => t.id === cur) ? cur : (survivors.length > 0 ? survivors[survivors.length - 1].id : null)));
        if (survivors.length === 0) setIsTerminalModalOpen(false);
        return survivors;
      });
    };
    void reconcile();
    const iv = window.setInterval(() => void reconcile(), 3000);
    return () => {
      cancelled = true;
      window.clearInterval(iv);
    };
  }, [hasInterceptTab]);

  const handleOpenTerminal = (cfg: TerminalSessionConfig) => {
    setTerminals((prev) => [...prev, cfg]);
    setActiveTermId(cfg.id);
    setIsTerminalModalOpen(true);
  };

  return (
    <TerminalContext.Provider value={{
      terminals, activeTermId, isTerminalModalOpen, handleOpenTerminal, setIsTerminalModalOpen, closeTerminal
    }}>
      {children}
      {terminals.length > 0 ? (
        <TerminalTabsModal
          isOpen={isTerminalModalOpen}
          terminals={terminals}
          activeTermId={activeTermId}
          sshUser={sshUser}
          recordSession={false}
          assistAvailable={!!meta?.terminal_assist_available}
          onSetActive={setActiveTermId}
          onCloseTerminal={closeTerminal}
          onCloseModal={() => setIsTerminalModalOpen(false)}
        />
      ) : null}
    </TerminalContext.Provider>
  );
}

export function useTerminal() {
  const ctx = useContext(TerminalContext);
  if (!ctx) throw new Error('useTerminal must be used within TerminalProvider');
  return ctx;
}
