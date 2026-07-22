import { cleanup, render, screen } from '@testing-library/react';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { useWorkspaceStore } from '../store';
import { useWizard } from '../../../RecipesTab/WizardContext';
import { RunPanel } from './RunPanel';

// StepRun itself drives a real network exec stream (cueExecStream) — replace
// it with a stub that reads the REAL (unmocked) WizardContext so the test can
// still prove the hosts RunPanel wired into the run actually reached the
// component that would have executed against them, without the real network
// call. This exercises RunPanel's WizardProvider wiring rather than papering
// over it with an inert stub.
vi.mock('../../../RecipesTab/StepRun', () => ({
  StepRun: () => {
    const { state } = useWizard();
    return <div>step-run for {state.hosts.length} hosts</div>;
  },
}));

const selectedRecords = [{ id: 'h1' }, { id: 'h2' }] as unknown as import('../../../HostPicker').HostRecord[];

vi.mock('../../../contexts/HostSelectionContext', () => ({
  useHostSelection: () => ({ records: [], selectedRecords, sshUser: 'root' }),
}));

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function props(): any {
  return { params: {}, api: {}, containerApi: {} };
}

afterEach(cleanup);

describe('RunPanel', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      docs: {
        'a.cue': {
          recipeId: 'a.cue', name: 'a',
          nodes: [{ id: 's1' }],
          edges: [],
          stepData: { s1: { kind: 'run', command: 'echo hi' } },
          recipeDefaults: {}, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
          runStepId: 's1', runCount: 1,
        },
      },
      active: 'a.cue', schema: {},
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
  });

  it('shows the empty state when no run has been triggered yet (runCount === 0)', () => {
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'a.cue': { ...s.docs['a.cue'], runStepId: null, runCount: 0 } },
    }));

    render(<RunPanel {...props()} />);

    expect(screen.getByText(/no active run/i)).toBeTruthy();
    expect(screen.queryByText(/step-run/)).toBeNull();
  });

  it('shows the empty state when there is no active doc', () => {
    useWorkspaceStore.setState({ active: null });

    render(<RunPanel {...props()} />);

    expect(screen.getByText(/no active run/i)).toBeTruthy();
  });

  it('renders StepRun (with the selected hosts reaching it) once a run is active', () => {
    render(<RunPanel {...props()} />);

    expect(screen.getByText('step-run for 2 hosts')).toBeTruthy();
    expect(screen.queryByText(/no active run/i)).toBeNull();
  });

  it('renders StepRun for a whole-recipe run (runStepId === null, runCount > 0)', () => {
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'a.cue': { ...s.docs['a.cue'], runStepId: null, runCount: 1 } },
    }));

    render(<RunPanel {...props()} />);

    expect(screen.getByText('step-run for 2 hosts')).toBeTruthy();
  });
});
