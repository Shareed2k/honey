import { cleanup, render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { useWorkspaceStore } from '../store';
import { RecordsPanel } from './RecordsPanel';

const recs = [
  { provider: 'docker', name: 'web', primary_ip: '10.0.0.1' },
  { provider: 'k8s', name: 'api', primary_ip: '10.0.0.2' },
];

// setSelectedKeys is a spy so tests can prove RecordsPanel drives the SHARED
// host-selection context (not local state) when a row is toggled.
const setSelectedKeys = vi.fn();

vi.mock('../../../contexts/HostSelectionContext', () => ({
  useHostSelection: () => ({
    records: recs,
    selectedKeys: {},
    setSelectedKeys,
    selectedRecords: [],
    sshUser: 'root',
  }),
}));

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function props(): any {
  return { params: {}, api: {}, containerApi: {} };
}

afterEach(cleanup);

describe('RecordsPanel', () => {
  beforeEach(() => {
    setSelectedKeys.mockReset();
    useWorkspaceStore.setState({ openTerminal: null });
  });

  it('lists every record from useHostSelection and renders a Terminal button per row', () => {
    render(<RecordsPanel {...props()} />);

    expect(screen.getByText('web')).toBeTruthy();
    expect(screen.getByText('api')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /terminal/i })).toHaveLength(2);
  });

  it('toggling a row checkbox calls setSelectedKeys (from the SHARED context) with a functional updater that toggles the key', () => {
    render(<RecordsPanel {...props()} />);

    const checkboxes = screen.getAllByRole('checkbox');
    // First checkbox in an antd Table with rowSelection is "select all" —
    // the row checkboxes follow it, one per record in row order.
    fireEvent.click(checkboxes[1]);

    expect(setSelectedKeys).toHaveBeenCalledTimes(1);
    // onToggleRow now passes a functional updater (not a plain map) so that
    // antd's select-all loop (HostPicker's onSelectAll calling onToggleRow
    // once per row, synchronously) doesn't clobber earlier toggles with a
    // stale render-closure `selectedKeys` — see RecordsPanel.selectAll.test.tsx
    // for the select-all regression this guards against.
    const updater = setSelectedKeys.mock.calls[0][0] as (
      prev: Record<string, boolean>,
    ) => Record<string, boolean>;
    expect(typeof updater).toBe('function');
    const result = updater({});
    expect(result['docker\x1eweb\x1e10.0.0.1']).toBe(true);
  });

  it('clicking Terminal calls the store openTerminal slot with that record', () => {
    const open = vi.fn();
    useWorkspaceStore.setState({ openTerminal: open });

    render(<RecordsPanel {...props()} />);

    const buttons = screen.getAllByRole('button', { name: /terminal/i });
    fireEvent.click(buttons[0]);

    expect(open).toHaveBeenCalledWith(expect.objectContaining({ name: 'web' }));
  });

  it('Terminal button is disabled when openTerminal is not yet wired (null)', () => {
    render(<RecordsPanel {...props()} />);

    const buttons = screen.getAllByRole('button', { name: /terminal/i });
    expect((buttons[0] as HTMLButtonElement).disabled).toBe(true);
  });
});
