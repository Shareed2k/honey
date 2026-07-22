import type { IDockviewPanelProps } from 'dockview';
import { TerminalSession } from '../../../TerminalModal';
import type { HostRecord } from '../../../HostPicker';
import { useHostSelection } from '../../../contexts/HostSelectionContext';

type TerminalParams = {
  record: HostRecord;
  pve?: 'serial' | 'vnc';
  truenasConsole?: 'ssh' | 'api';
};

// Re-hosts the existing TerminalSession component (SSH/docker/k8s/PVE/
// TrueNAS/VNC) inside a dockview panel. TerminalSession owns its own
// xterm/WebSocket lifecycle via useEffect cleanup (see TerminalModal.tsx) —
// so disposal on panel close is just React unmount, nothing extra needed
// here. Spawned by the shell's `openTerminal` store slot (Task 12), one
// panel per session.
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
        registerCloseTabSender={() => {}}
      />
    </div>
  );
}
