import { cleanup, render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { message } from 'antd';

// Non-empty so the Open Select's fetched-options path (GET /api/v1/recipes/store)
// has something to render/select in the Open-flow tests below. `apiPutJson` is
// stubbed too — the shell wires up `attachWorkspaceSync` on mount, which PUTs
// the workspace back after a debounce; without a real export here that PUT
// would throw `apiPutJson is not a function` if its timer ever fired before
// `afterEach(cleanup)` tears the component (and workspaceSync) down.
vi.mock('../../api/core', () => ({
  apiGet: vi.fn(async (path: string) => ({
    ok: true,
    json: async () => (path.includes('schema') ? {} : [{ name: 'deploy.cue' }]),
  })),
  apiPutJson: vi.fn(async () => ({ ok: true })),
}));
vi.mock('../../contexts/AppContext', () => ({
  useAppContext: () => ({ meta: { version: '1' } }),
}));
// store.createDoc(name) calls parseDiskRecipe under the hood — stub it so the
// Open flow resolves without a real network/backend round trip. Keep every
// other export real (e.g. listStepKinds, used by the always-mounted
// ToolboxPanel) via importOriginal, mirroring ToolboxPanel.test.tsx.
vi.mock('../../api/recipes', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/recipes')>();
  return {
    ...actual,
    parseDiskRecipe: vi.fn(async () => ({
      name: 'deploy.cue',
      defaults: {},
      steps: [{ id: 'step1', kind: 'command', host: '_', command: 'echo hi' }],
    })),
  };
});

import StudioWorkspace from '../StudioWorkspace';
import { useWorkspaceStore } from './store';
import { HostSelectionProvider } from '../../contexts/HostSelectionContext';

beforeEach(() => {
  useWorkspaceStore.setState({ docs: {}, active: null, schema: null });
});

afterEach(cleanup);

// The Run panel (mounted as part of the shell's onReady layout) reads
// useHostSelection(), which throws outside a HostSelectionProvider — in the
// real app this comes for free from contexts/index.tsx's RootProvider (which
// wraps the whole app, StudioWorkspace included). Reproduce that ancestor
// here so the shell renders exactly as it does in production.
function renderShell() {
  return render(
    <HostSelectionProvider>
      <StudioWorkspace />
    </HostSelectionProvider>,
  );
}

describe('StudioWorkspace shell', () => {
  it('opening a recipe adds a graph panel and sets it active', async () => {
    renderShell();

    fireEvent.click(await screen.findByRole('button', { name: /new recipe/i }));

    await waitFor(() => {
      const ids = Object.keys(useWorkspaceStore.getState().docs);
      expect(ids.some((id) => id.startsWith('untitled-'))).toBe(true);
    });

    await waitFor(() => {
      expect(document.querySelector('.react-flow')).toBeTruthy();
    });
  });

  it('Open select: choosing a fetched recipe calls createDoc and opens its graph panel', async () => {
    renderShell();

    // The recipe-store list is fetched on mount; wait for the option to land
    // in the Select before trying to open it. antd/rc-select opens its
    // dropdown on mousedown (not click), and closes on the option's real
    // click only if that click reaches the option node directly — driving
    // both steps via fireEvent (rather than userEvent, whose fuller
    // pointer/focus choreography raced rc-select's outside-click-close
    // handling and dropped the selection in local testing) is what proved
    // reliable here.
    const combobox = await screen.findByRole('combobox');
    fireEvent.mouseDown(combobox);
    // The option renders as a `title="deploy.cue"` div wrapping a
    // `.ant-select-item-option-content` child that repeats the same text —
    // findByTitle avoids the ambiguous multi-match a text/role query hits.
    const option = await screen.findByTitle('deploy.cue');
    fireEvent.click(option);

    await waitFor(() => {
      expect(useWorkspaceStore.getState().docs['deploy.cue']).toBeTruthy();
    });

    await waitFor(() => {
      expect(document.querySelector('.react-flow')).toBeTruthy();
    });
  });

  it('Open select: a createDoc failure surfaces an error toast and opens no doc/graph', async () => {
    const errSpy = vi.spyOn(message, 'error').mockImplementation(() => ({}) as never);
    const createDocSpy = vi
      .spyOn(useWorkspaceStore.getState(), 'createDoc')
      .mockRejectedValue(new Error('boom'));

    renderShell();

    const combobox = await screen.findByRole('combobox');
    fireEvent.mouseDown(combobox);
    const option = await screen.findByTitle('deploy.cue');
    fireEvent.click(option);

    await waitFor(() => {
      expect(errSpy).toHaveBeenCalled();
    });
    expect(errSpy.mock.calls[0]?.[0]).toContain('deploy.cue');

    expect(useWorkspaceStore.getState().docs['deploy.cue']).toBeFalsy();
    expect(document.querySelector('.react-flow')).toBeFalsy();

    createDocSpy.mockRestore();
    errSpy.mockRestore();
  });
});
