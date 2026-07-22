import { cleanup, render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { useWorkspaceStore } from '../store';
import { StepEditorPanel } from './StepEditorPanel';

// Stub renders a button that fires onChange with a sentinel value so we can
// prove setStepData is wired with the right (recipeId, nodeId) pair — a
// swapped-arg regression would edit the wrong doc/node and this test would fail.
vi.mock('../../DynamicStepForm', () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  default: (p: any) => (
    <div>
      <span data-testid="schema">{JSON.stringify(p.schema)}</span>
      <span data-testid="value">{JSON.stringify(p.value)}</span>
      <button onClick={() => p.onChange({ kind: 'run', command: 'X' })}>edit-step</button>
    </div>
  ),
}));

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function props(): any {
  return { params: {}, api: {}, containerApi: {} };
}

afterEach(cleanup);

describe('StepEditorPanel', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      docs: {
        'a.cue': {
          recipeId: 'a.cue', name: 'a', nodes: [], edges: [],
          stepData: { node1: { kind: 'run' } },
          recipeDefaults: {}, selectedNodeId: 'node1', rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
        },
        'b.cue': {
          recipeId: 'b.cue', name: 'b', nodes: [], edges: [],
          stepData: { node1: { kind: 'run' } },
          recipeDefaults: {}, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
        },
      },
      active: 'a.cue', schema: {},
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
  });

  it('shows a placeholder when there is no selected step', () => {
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'a.cue': { ...s.docs['a.cue'], selectedNodeId: null } },
    }));
    render(<StepEditorPanel {...props()} />);
    expect(screen.getByText('Select a step in the graph.')).toBeTruthy();
  });

  it('routes edits through setStepData with the active doc id + selected node id, leaving other docs untouched', () => {
    render(<StepEditorPanel {...props()} />);

    fireEvent.click(screen.getByRole('button', { name: 'edit-step' }));

    expect(useWorkspaceStore.getState().docs['a.cue'].stepData['node1']).toEqual({
      kind: 'run',
      command: 'X',
    });
    // cross-doc isolation: doc 'b.cue' must be untouched by the edit on 'a.cue'.
    expect(useWorkspaceStore.getState().docs['b.cue'].stepData['node1']).toEqual({ kind: 'run' });
  });

  it('passes the resolved schema and current value down to DynamicStepForm', () => {
    render(<StepEditorPanel {...props()} />);
    expect(screen.getByTestId('schema').textContent).toBe(JSON.stringify({}));
    expect(screen.getByTestId('value').textContent).toBe(JSON.stringify({ kind: 'run' }));
  });
});
