import { cleanup, render } from '@testing-library/react';
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';

// Captures every props object TerminalSession receives across renders, in
// order, so tests can assert on prop identity stability (not just content) —
// e.g. that registerCloseTabSender keeps the same reference across parent
// re-renders instead of tearing down the live ws+xterm session.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const capturedProps: any[] = [];

// TerminalSession's own xterm/WebSocket disposal lives inside its useEffect
// cleanup (see TerminalModal.tsx) — TerminalPanel just renders it and lets
// React unmount trigger that cleanup. Stubbing it here keeps this test
// focused on TerminalPanel's own wiring (param plumbing + mount/unmount),
// not xterm/WS internals already covered by TerminalModal's own tests.
vi.mock('../../../TerminalModal', () => ({
  TerminalSession: (props: {
    record?: { name?: string };
    pveConsole?: string;
    registerCloseTabSender?: (...args: unknown[]) => void;
  }) => {
    capturedProps.push(props);
    return (
      <div data-testid="session">
        session:{props.record?.name}:{props.pveConsole}
      </div>
    );
  },
}));

vi.mock('../../../contexts/HostSelectionContext', () => ({
  useHostSelection: () => ({ sshUser: 'root' }),
}));

import { TerminalPanel } from './TerminalPanel';

afterEach(cleanup);

beforeEach(() => {
  capturedProps.length = 0;
});

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

  // Regression for a critical bug: TerminalPanel used to pass
  // registerCloseTabSender={() => {}} — a fresh inline arrow every render.
  // TerminalSession's connect effect (TerminalModal.tsx) lists
  // registerCloseTabSender in its useEffect dependency array, so a new
  // reference each render re-ran that effect and tore down/reconnected the
  // live ws+xterm session (ws.close(); term.dispose()) any time TerminalPanel
  // re-rendered for any unrelated reason (e.g. HostSelectionContext's
  // unmemoized Provider value changing on any host-selection state update).
  // A parent re-render (simulated here via rerender() with equivalent props,
  // which unconditionally re-invokes this unmemoized function component)
  // must NOT produce a new registerCloseTabSender reference.
  it('passes a stable registerCloseTabSender reference across re-renders', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const props: any = {
      params: { record: { name: 'web', provider: 'ssh', primary_ip: '10.0.0.1' }, pve: 'serial' },
      api: { id: 'term:stable' },
      containerApi: {},
    };
    const { rerender } = render(<TerminalPanel {...props} />);
    rerender(<TerminalPanel {...props} />);

    expect(capturedProps.length).toBe(2);
    const firstRef = capturedProps[0].registerCloseTabSender;
    const secondRef = capturedProps[1].registerCloseTabSender;
    expect(firstRef).toBeTruthy();
    expect(firstRef).toBe(secondRef);
  });
});
