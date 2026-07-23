import { cleanup, render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { useWorkspaceStore } from '../store';
import { SettingsPanel } from './SettingsPanel';

// Stub renders a button that fires onChange with a sentinel value so we can
// prove setRecipeDefaults is wired with the right (recipeId, value) pair —
// mirrors StepEditorPanel.test.tsx's DynamicStepForm stub.
vi.mock('../../DynamicStepForm', () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  default: (p: any) => (
    <div>
      <span data-testid="schema">{JSON.stringify(p.schema)}</span>
      <span data-testid="value">{JSON.stringify(p.value)}</span>
      <button onClick={() => p.onChange({ retries: 5 })}>edit-defaults</button>
    </div>
  ),
}));

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function props(): any {
  return { params: {}, api: {}, containerApi: {} };
}

afterEach(cleanup);

describe('SettingsPanel', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      docs: {
        'a.cue': {
          recipeId: 'a.cue', name: 'a', nodes: [], edges: [], stepData: {},
          recipeDefaults: { timeout: 30 }, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
        },
        'b.cue': {
          recipeId: 'b.cue', name: 'b', nodes: [], edges: [], stepData: {},
          recipeDefaults: { timeout: 99 }, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
        },
      },
      active: 'a.cue', schema: { definitions: { defaults: { properties: {} } } },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
  });

  it('shows an empty state when there is no active document', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    useWorkspaceStore.setState({ active: null } as any);
    render(<SettingsPanel {...props()} />);
    expect(screen.getByText(/no active document/i)).toBeTruthy();
  });

  it('renders DynamicStepForm with the active doc\'s recipeDefaults as its value', () => {
    render(<SettingsPanel {...props()} />);
    expect(screen.getByTestId('value').textContent).toBe(JSON.stringify({ timeout: 30 }));
  });

  it('onChange calls setRecipeDefaults with the active doc id and the new value', () => {
    render(<SettingsPanel {...props()} />);

    fireEvent.click(screen.getByRole('button', { name: 'edit-defaults' }));

    expect(useWorkspaceStore.getState().docs['a.cue'].recipeDefaults).toEqual({ retries: 5 });
    expect(useWorkspaceStore.getState().docs['a.cue'].dirty).toBe(true);
    // cross-doc isolation: doc 'b.cue' must be untouched by the edit on 'a.cue'.
    expect(useWorkspaceStore.getState().docs['b.cue'].recipeDefaults).toEqual({ timeout: 99 });
  });
});
