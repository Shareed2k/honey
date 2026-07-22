import { cleanup, render } from '@testing-library/react';
import { describe, it, expect, vi, afterEach } from 'vitest';

// TerminalSession's own xterm/WebSocket disposal lives inside its useEffect
// cleanup (see TerminalModal.tsx) — TerminalPanel just renders it and lets
// React unmount trigger that cleanup. Stubbing it here keeps this test
// focused on TerminalPanel's own wiring (param plumbing + mount/unmount),
// not xterm/WS internals already covered by TerminalModal's own tests.
vi.mock('../../../TerminalModal', () => ({
  TerminalSession: (props: { record?: { name?: string }; pveConsole?: string }) => (
    <div data-testid="session">
      session:{props.record?.name}:{props.pveConsole}
    </div>
  ),
}));

vi.mock('../../../contexts/HostSelectionContext', () => ({
  useHostSelection: () => ({ sshUser: 'root' }),
}));

import { TerminalPanel } from './TerminalPanel';

afterEach(cleanup);

describe('TerminalPanel', () => {
  it('renders the session for its record and pve mode from params', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = {
      params: { record: { name: 'web', provider: 'ssh', primary_ip: '10.0.0.1' }, pve: 'serial' },
      api: { id: 'term:abc' },
      containerApi: {},
    };
    const { getByTestId } = render(<TerminalPanel {...props} />);
    expect(getByTestId('session').textContent).toContain('web');
    expect(getByTestId('session').textContent).toContain('serial');
  });

  it('unmounts without throwing (session cleanup runs via React unmount)', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = {
      params: { record: { name: 'db', provider: 'ssh', primary_ip: '10.0.0.2' }, pve: 'vnc' },
      api: { id: 'term:def' },
      containerApi: {},
    };
    const { unmount } = render(<TerminalPanel {...props} />);
    expect(() => unmount()).not.toThrow();
  });
});
