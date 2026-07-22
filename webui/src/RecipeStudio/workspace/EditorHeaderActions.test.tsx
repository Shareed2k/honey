import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider, theme } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { message } from 'antd';
import type { IDockviewHeaderActionsProps, IDockviewPanel } from 'dockview';
import { useWorkspaceStore } from './store';
import { EditorHeaderActions } from './EditorHeaderActions';

// Vitest hoists vi.mock factories above other module-level statements, so a
// plain `const` referenced from inside the factory would hit a temporal-dead-
// zone error — the "mock"-prefixed name opts it out of that hoisting check.
const mockHostSelectionState: { selectedRecords: unknown[] } = { selectedRecords: [] };
vi.mock('../../contexts/HostSelectionContext', () => ({
  useHostSelection: () => ({ records: [], selectedRecords: mockHostSelectionState.selectedRecords, sshUser: 'root' }),
}));

// antd Modal (rendered inside EditorHeaderActions via StorageModal) reads
// getComputedStyle for its motion/measurement logic — mirrors the same
// mock StorageModal.test.tsx / GitLoadModal.test.tsx use.
const originalGetComputedStyle = window.getComputedStyle.bind(window);

beforeEach(() => {
  vi.spyOn(window, 'getComputedStyle').mockImplementation((elt) => originalGetComputedStyle(elt));
  mockHostSelectionState.selectedRecords = [];
  useWorkspaceStore.setState({
    docs: {
      'deploy.cue': {
        recipeId: 'deploy.cue', name: 'deploy', nodes: [], edges: [], stepData: {},
        recipeDefaults: {}, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
        validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
      },
    },
    active: 'deploy.cue', schema: null,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  } as any);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

// Minimal fake satisfying only the fields EditorHeaderActions reads
// (`activePanel.id`). Cast through `unknown` (never `any`) so the object
// literal is still type-checked against the real dockview surface it stands
// in for, mirroring registry.test.ts's convention.
function headerProps(activePanelId: string | undefined): IDockviewHeaderActionsProps {
  const activePanel = activePanelId
    ? ({ id: activePanelId } as unknown as IDockviewPanel)
    : undefined;
  return {
    api: {} as unknown as IDockviewHeaderActionsProps['api'],
    containerApi: {} as unknown as IDockviewHeaderActionsProps['containerApi'],
    panels: [],
    activePanel,
    isGroupActive: true,
    group: {} as unknown as IDockviewHeaderActionsProps['group'],
  };
}

function renderWithTheme(activePanelId: string | undefined) {
  return render(
    <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
      <EditorHeaderActions {...headerProps(activePanelId)} />
    </ConfigProvider>,
  );
}

describe('EditorHeaderActions', () => {
  it('renders nothing when the active panel is not a recipe editor panel', () => {
    const { container } = renderWithTheme('toolbox');
    expect(container.innerHTML).toBe('');
    expect(screen.queryAllByRole('button')).toHaveLength(0);
  });

  it('renders nothing when the active panel has no matching open doc', () => {
    const { container } = renderWithTheme('graph:missing.cue');
    expect(container.innerHTML).toBe('');
  });

  it('renders Raw/Validate/Save when the active panel is a recipe editor panel with an open doc', () => {
    renderWithTheme('graph:deploy.cue');
    expect(screen.getByRole('button', { name: /raw/i })).toBeTruthy();
    expect(screen.getByRole('button', { name: /validate/i })).toBeTruthy();
    expect(screen.getByRole('button', { name: /save/i })).toBeTruthy();
  });

  it('clicking Validate calls store.validate for the panel\'s recipe id', async () => {
    const validateSpy = vi.spyOn(useWorkspaceStore.getState(), 'validate').mockResolvedValue(undefined);
    renderWithTheme('graph:deploy.cue');

    fireEvent.click(screen.getByRole('button', { name: /validate/i }));

    await waitFor(() => expect(validateSpy).toHaveBeenCalledWith('deploy.cue'));
  });

  it('clicking Raw calls store.switchToRaw when the doc is not already in raw mode', async () => {
    const switchToRawSpy = vi.spyOn(useWorkspaceStore.getState(), 'switchToRaw').mockResolvedValue(undefined);
    renderWithTheme('graph:deploy.cue');

    fireEvent.click(screen.getByRole('button', { name: /raw/i }));

    await waitFor(() => expect(switchToRawSpy).toHaveBeenCalledWith('deploy.cue'));
  });

  it('clicking Visual calls store.switchToVisual when the doc is already in raw mode', () => {
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'deploy.cue': { ...s.docs['deploy.cue'], rawMode: true } },
    }));
    const switchToVisualSpy = vi.spyOn(useWorkspaceStore.getState(), 'switchToVisual').mockImplementation(() => {});
    renderWithTheme('graph:deploy.cue');

    fireEvent.click(screen.getByRole('button', { name: /visual/i }));

    expect(switchToVisualSpy).toHaveBeenCalledWith('deploy.cue');
  });

  it('clicking Save opens the StorageModal', async () => {
    renderWithTheme('graph:deploy.cue');

    expect(screen.queryByText('Save Recipe Draft')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: /save/i }));

    expect(await screen.findByText('Save Recipe Draft')).toBeTruthy();
  });

  describe('Run recipe', () => {
    it('warns and does not start a run when no hosts are selected', () => {
      mockHostSelectionState.selectedRecords = [];
      const startRunSpy = vi.spyOn(useWorkspaceStore.getState(), 'startRun');
      const warnSpy = vi.spyOn(message, 'warning').mockImplementation(() => '' as unknown as ReturnType<typeof message.warning>);

      renderWithTheme('graph:deploy.cue');
      fireEvent.click(screen.getByRole('button', { name: /run recipe/i }));

      expect(warnSpy).toHaveBeenCalledWith('Select hosts in the Records panel first');
      expect(startRunSpy).not.toHaveBeenCalled();

      startRunSpy.mockRestore();
      warnSpy.mockRestore();
    });

    it('starts a whole-recipe run (stepId null) for the panel\'s recipe id when hosts are selected', () => {
      mockHostSelectionState.selectedRecords = [{ id: 'h1' }];
      const startRunSpy = vi.spyOn(useWorkspaceStore.getState(), 'startRun').mockImplementation(() => {});

      renderWithTheme('graph:deploy.cue');
      fireEvent.click(screen.getByRole('button', { name: /run recipe/i }));

      expect(startRunSpy).toHaveBeenCalledWith('deploy.cue', null);

      startRunSpy.mockRestore();
    });
  });
});
