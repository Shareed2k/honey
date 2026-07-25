import { useEffect } from 'react';
import { cleanup, render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { useWorkspaceStore } from '../store';
import { RecordsPanel } from './RecordsPanel';
import { HostSelectionProvider, useHostSelection } from '../../../contexts/HostSelectionContext';

// HostSelectionProvider fetches /api/v1/providers on mount; stub it so the
// real provider (used below, unlike RecordsPanel.test.tsx's mocked context)
// doesn't hit a real network call in jsdom.
vi.mock('../../../api/core', () => ({
  apiGet: vi.fn(async () => ({ ok: true, json: async () => ({ providers: [] }) })),
}));

const recs = [
  { provider: 'docker', name: 'web', primary_ip: '10.0.0.1' },
  { provider: 'k8s', name: 'api', primary_ip: '10.0.0.2' },
  { provider: 'proxmox', name: 'db', primary_ip: '10.0.0.3' },
];

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function props(): any {
  return { params: {}, api: {}, containerApi: {} };
}

// There's no external prop to seed `records` into HostSelectionProvider (in
// the real app they arrive from a search fetch) — this harness reaches into
// the SHARED context via useHostSelection() to seed them on mount, and
// surfaces selectedRecords.length so the test can assert on the context's
// derived selection rather than reimplementing it.
function TestHarness() {
  const { setRecords, selectedRecords } = useHostSelection();
  useEffect(() => {
    setRecords(recs);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return (
    <>
      <div data-testid="selected-count">{selectedRecords.length}</div>
      <RecordsPanel {...props()} />
    </>
  );
}

afterEach(cleanup);

describe('RecordsPanel select-all (regression)', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({ openTerminal: null });
  });

  it('clicking the header "select all" checkbox selects ALL records, not just the last one', async () => {
    render(
      <HostSelectionProvider>
        <TestHarness />
      </HostSelectionProvider>,
    );

    // Wait for the seeded records to actually render before interacting.
    await screen.findByText('web');
    await screen.findByText('api');
    await screen.findByText('db');

    const checkboxes = screen.getAllByRole('checkbox');
    // antd Table's header "select all" checkbox is always first, ahead of
    // the per-row checkboxes (see RecordsPanel.test.tsx for the same
    // convention).
    fireEvent.click(checkboxes[0]);

    expect(screen.getByTestId('selected-count').textContent).toBe('3');
  });
});
