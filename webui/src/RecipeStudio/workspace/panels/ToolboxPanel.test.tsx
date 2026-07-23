import { cleanup, render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { useWorkspaceStore } from '../store';
import { ToolboxPanel } from './ToolboxPanel';

vi.mock('../../../api/recipes', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../api/recipes')>();
  return { ...actual, listStepKinds: () => [{ kind: 'run', label: 'Run Command' }] };
});

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function props(): any {
  return { params: {}, api: {}, containerApi: {} };
}

afterEach(cleanup);

describe('ToolboxPanel', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      docs: {
        'a.cue': {
          recipeId: 'a.cue', name: 'a', nodes: [], edges: [], stepData: {},
          recipeDefaults: {}, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
        },
      },
      active: 'a.cue', schema: {},
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
  });

  it('adds a step to the active doc when a kind button is clicked', () => {
    render(<ToolboxPanel {...props()} />);
    fireEvent.click(screen.getByRole('button', { name: /Run Command/ }));
    expect(Object.keys(useWorkspaceStore.getState().docs['a.cue'].stepData)).toHaveLength(1);
  });

  it('is disabled/empty when no doc is active', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    useWorkspaceStore.setState({ active: null } as any);
    render(<ToolboxPanel {...props()} />);
    expect(screen.getByText(/open a recipe/i)).toBeTruthy();
    // the snippet insert control must not render at all with no active doc —
    // there's nothing for it to insert into.
    expect(screen.queryByText(/insert snippet/i)).toBeNull();
  });

  describe('snippet insert', () => {
    it('selecting a snippet calls store.addSnippet with the active doc id', () => {
      const addSnippetSpy = vi.spyOn(useWorkspaceStore.getState(), 'addSnippet');
      render(<ToolboxPanel {...props()} />);

      fireEvent.mouseDown(screen.getByText(/insert snippet/i));
      const option = screen.getByTitle('Load Check + AI');
      fireEvent.click(option);

      expect(addSnippetSpy).toHaveBeenCalledWith('a.cue', 'load_check_ai');
      addSnippetSpy.mockRestore();
    });

    it('actually inserts the snippet steps into the active doc (real store call, not just spied)', () => {
      render(<ToolboxPanel {...props()} />);

      fireEvent.mouseDown(screen.getByText(/insert snippet/i));
      fireEvent.click(screen.getByTitle('Load Check + AI'));

      expect(Object.keys(useWorkspaceStore.getState().docs['a.cue'].stepData).length).toBeGreaterThan(0);
    });
  });

  describe('Reset canvas', () => {
    it('renders a Reset canvas control that calls resetDoc after confirming', async () => {
      const resetDocSpy = vi.spyOn(useWorkspaceStore.getState(), 'resetDoc').mockImplementation(() => {});
      render(<ToolboxPanel {...props()} />);

      fireEvent.click(screen.getByRole('button', { name: /reset canvas/i }));
      // antd Modal.confirm renders its own dialog with an OK button ("Reset",
      // an exact-string match so it doesn't also match the "Reset canvas"
      // trigger button above) — confirm it.
      fireEvent.click(await screen.findByRole('button', { name: 'Reset' }));

      expect(resetDocSpy).toHaveBeenCalledWith('a.cue');
      resetDocSpy.mockRestore();
    });
  });
});
