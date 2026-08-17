import { createContext, useContext, useState, useEffect, type ReactNode } from 'react';
import { TerminalTabsModal, type TerminalSessionConfig, type PveConsoleMode, type TrueNASConsoleMode } from '../TerminalModal';
import type { InterceptOptions } from '../api/intercept';
import { useHostSelection } from './HostSelectionContext';
import { useAppContext } from './AppContext';
import { recordKey } from '../HostPicker';

interface TerminalContextType {
  terminals: TerminalSessionConfig[];
  activeTermId: string | null;
  isTerminalModalOpen: boolean;
  handleOpenTerminal: (cfg: TerminalSessionConfig) => void;
  setIsTerminalModalOpen: (open: boolean) => void;
}

const TerminalContext = createContext<TerminalContextType | null>(null);

function updateUrlParam(key: string, value: string | null) {
  const params = new URLSearchParams(window.location.search);
  if (value === null) params.delete(key);
  else params.set(key, value);
  window.history.replaceState(null, '', `?${params.toString()}`);
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
        const rKey = (t.record as { _key?: string })._key || recordKey(t.record);
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

  const handleOpenTerminal = (cfg: TerminalSessionConfig) => {
    setTerminals((prev) => [...prev, cfg]);
    setActiveTermId(cfg.id);
    setIsTerminalModalOpen(true);
  };

  return (
    <TerminalContext.Provider value={{
      terminals, activeTermId, isTerminalModalOpen, handleOpenTerminal, setIsTerminalModalOpen
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
    </TerminalContext.Provider>
  );
}

export function useTerminal() {
  const ctx = useContext(TerminalContext);
  if (!ctx) throw new Error('useTerminal must be used within TerminalProvider');
  return ctx;
}
