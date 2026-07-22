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
  });
});
