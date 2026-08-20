import { createContext, useContext, useState, useEffect, useRef, useCallback, type ReactNode } from 'react';
import { TerminalTabsModal, type TerminalSessionConfig, type PveConsoleMode, type TrueNASConsoleMode } from '../TerminalModal';
import { type InterceptOptions, type InterceptSession, fetchInterceptSessions, sessionPodKey, recordPodKey } from '../api/intercept';
import { useHostSelection } from './HostSelectionContext';
import { useAppContext } from './AppContext';
import { recordKey } from '../HostPicker';
import { ShareAccessModal } from '../tabs/ShareAccessModal';

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
  // interceptSessions is the single source of truth for the server's active
  // interception list: the SearchTab badge/panel, the Reattach button, and the
  // dead-tab reconcile all read this one polled state so they can never
  // disagree with each other.
  interceptSessions: InterceptSession[];
  refreshInterceptSessions: () => void;
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

  // interceptSessions is polled here — the ONE place — so every consumer (the
  // SearchTab badge/panel, the Reattach button, the dead-tab reconcile below)
  // reads the same list and can't disagree. Polling is unconditional: the list
  // must stay fresh even with no open tab (a session started elsewhere, or one
  // whose only tab just closed), otherwise a stopped/gone intercept lingers in
  // the panel until a manual refresh.
  const [interceptSessions, setInterceptSessions] = useState<InterceptSession[]>([]);
  const refreshInterceptSessions = useCallback(() => {
    void fetchInterceptSessions().then(setInterceptSessions).catch(() => { /* keep last list */ });
  }, []);
  useEffect(() => {
    refreshInterceptSessions();
    const iv = window.setInterval(refreshInterceptSessions, 3000);
    return () => window.clearInterval(iv);
  }, [refreshInterceptSessions]);

  const closeTerminal = useCallback((id: string) => {
    sessionStorage.removeItem(`honey_term_${id}`);
    let wasIntercept = false;
    setTerminals((prev) => {
      wasIntercept = prev.some((t) => t.id === id && !!t.intercept);
      const next = prev.filter((t) => t.id !== id);
      setActiveTermId((cur) => (cur === id ? (next.length > 0 ? next[next.length - 1].id : null) : cur));
      if (next.length === 0) setIsTerminalModalOpen(false);
      return next;
    });
    // Closing an intercept tab via the × sends close_tab, which the server turns
    // into tmux kill-session — so the session leaves the list. Refresh once the
    // kill has had a moment to land (the 3s poll is the backstop).
    if (wasIntercept) window.setTimeout(refreshInterceptSessions, 600);
  }, [refreshInterceptSessions]);

  // Reconcile open intercept tabs against interceptSessions. An intercept
  // session survives a browser refresh (the tmux pane keeps running), so a
  // closed WebSocket alone must NOT close its tab. The list is the truth: once
  // a tab's session has been seen active and then disappears (Stop, the shell
  // exiting, or the pane dying), the session is gone for good — drop the
  // now-dead tab. A tab whose session has never appeared yet (just opened, poll
  // still stale) is left alone via the seen-key gate.
  const seenKeysRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    const active = new Set(interceptSessions.map(sessionPodKey));
    active.forEach((k) => seenKeysRef.current.add(k));
    setTerminals((prev) => {
      const survivors = prev.filter((t) => {
        if (!t.intercept) return true;
        const key = recordPodKey(t.record);
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
  }, [interceptSessions]);

  const handleOpenTerminal = (cfg: TerminalSessionConfig) => {
    setTerminals((prev) => [...prev, cfg]);
    setActiveTermId(cfg.id);
    setIsTerminalModalOpen(true);
  };

  // "Share this terminal" — the tab being shared, so ShareAccessModal can be
  // pre-seeded with its live mux_session (the server-side tmux/zellij session
  // name a redeemed grant attaches to; see internal/webserver/pty_proxy.go).
  const [shareTarget, setShareTarget] = useState<TerminalSessionConfig | null>(null);
  // An SSH web-tty tab's mux name is "honey_" + its id (see ptyMuxSessionName
  // server-side): the id is a crypto.randomUUID(), whose characters are all in
  // ptyMuxSessionName's allowed set, so it passes through unchanged and this
  // stays a pure client-side computation. An intercept tab's mux name
  // (honey-int-<hex>) is a server-computed digest of pod identity that the
  // client never sees directly — it is read back from the one place that
  // carries it: the polled interceptSessions list's `id` field (the server's
  // webInterceptView.ID, which IS the tmux session name).
  const liveMuxSession = (t: TerminalSessionConfig): string | null => {
    if (!t.intercept) return `honey_${t.id}`;
    const key = recordPodKey(t.record);
    return interceptSessions.find((s) => sessionPodKey(s) === key)?.id ?? null;
  };
  const shareMuxSession = shareTarget ? liveMuxSession(shareTarget) : null;

  return (
    <TerminalContext.Provider value={{
      terminals, activeTermId, isTerminalModalOpen, handleOpenTerminal, setIsTerminalModalOpen, closeTerminal,
      interceptSessions, refreshInterceptSessions,
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
          onShareTerminal={setShareTarget}
        />
      ) : null}
      <ShareAccessModal
        record={shareTarget?.record ?? null}
        open={!!shareTarget && !!shareMuxSession}
        onClose={() => setShareTarget(null)}
        liveSession={shareMuxSession ? { muxSession: shareMuxSession } : null}
      />
    </TerminalContext.Provider>
  );
}

export function useTerminal() {
  const ctx = useContext(TerminalContext);
  if (!ctx) throw new Error('useTerminal must be used within TerminalProvider');
  return ctx;
}
