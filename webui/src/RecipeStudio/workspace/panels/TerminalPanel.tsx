import type { IDockviewPanelProps } from 'dockview-react';
import { TerminalSession, type PveConsoleMode, type TrueNASConsoleMode } from '../../../TerminalModal';
import type { HostRecord } from '../../../HostPicker';
import { useHostSelection } from '../../../contexts/HostSelectionContext';

type TerminalParams = {
  record: HostRecord;
  pve?: PveConsoleMode;
  truenasConsole?: TrueNASConsoleMode;
};

// Stable no-op passed as registerCloseTabSender. TerminalSession's connect
// effect (TerminalModal.tsx) lists registerCloseTabSender in its dependency
// array — a fresh inline arrow on every TerminalPanel render (e.g. from
// HostSelectionContext's unmemoized Provider value re-rendering this panel)
// would re-run that effect and tear down/reconnect the live ws+xterm session.
// A module-level const guarantees the same reference across renders.
const NOOP = () => {};

// Hosts the TerminalSession component (SSH/docker/k8s/PVE/TrueNAS/VNC) inside
// a dockview panel. TerminalSession owns its own xterm/WebSocket lifecycle via
// useEffect cleanup (see TerminalModal.tsx) — so disposal on panel close is
// just React unmount, nothing extra needed here. Spawned by the shell's
// `openTerminal` store slot (StudioWorkspace.tsx's `onReady`), which mints a
// fresh `term:<uuid>` panel id on every call — each open is a brand new
// session, never a re-host of an existing one, so multiple sessions can be
// open concurrently.
export function TerminalPanel({ params, api }: IDockviewPanelProps<TerminalParams>) {
  const { sshUser } = useHostSelection();

  return (
    <div style={{ height: '100%', width: '100%' }}>
      <TerminalSession
        sessionId={api.id}
        record={params.record}
        sshUser={sshUser}
        recordSession={false}
        assistAvailable={false}
        pveConsole={params.pve ?? 'serial'}
        truenasConsole={params.truenasConsole ?? 'ssh'}
        isActive={true}
        registerCloseTabSender={NOOP}
      />
    </div>
  );
}
