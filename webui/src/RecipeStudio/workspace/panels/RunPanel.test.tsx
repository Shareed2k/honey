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
//
// The stub also renders a deterministic summary of the recipe RunPanel built
// (`state.edits`, seeded as WizardProvider's `initialEdits`) — one
// `id:command:depends` segment per step, joined by `|`, in step order — so
// tests can assert on the ACTUAL subgraph-filtered recipe StepRun received,
// not just the host count.
vi.mock('../../../RecipesTab/StepRun', () => ({
  StepRun: () => {
    const { state } = useWizard();
    const recipe = state.edits as { steps?: Array<Record<string, unknown>> } | null;
    const steps = recipe?.steps ?? [];
    const summary = steps
      .map((s) => {
        const depends = Array.isArray(s.depends) ? (s.depends as string[]).join(',') : '';
        return `${s.id}:${s.command}:${depends}`;
      })
      .join('|');
    return (
      <div>
        <div>step-run for {state.hosts.length} hosts</div>
        <div data-testid="captured-recipe">{summary}</div>
      </div>
    );
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

// Highest-risk untested behavior on the Run panel: the recipe RunPanel builds
// and hands to StepRun must be filtered to the target step + its ancestors
// for a "Run Step" trigger, and must be the whole recipe for a "Run recipe"
// trigger. buildRunRecipe (RunPanel.tsx) is a thin wrapper around
// collectAncestorNodeIDs + buildRecipeFromFlow, but neither prior test here
// asserted on the actual steps produced — only on `hosts.length`. A filter
// that dropped the target, kept a wrong ancestor, or leaked descendants would
// have passed every existing test in this file.
//
// Graph: step_a -> step_b -> step_c (edges a->b, b->c), one `run` step each
// with a distinguishing `command`, so step identity is provable from the
// recipe StepRun actually received (captured via the WizardContext stub
// above, not by re-deriving expectations from RunPanel's own logic).
describe('RunPanel subgraph filtering (step = target + ancestors, whole = all)', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      docs: {
        'graph.cue': {
          recipeId: 'graph.cue', name: 'graph',
          nodes: [{ id: 'step_a' }, { id: 'step_b' }, { id: 'step_c' }],
          edges: [
            { source: 'step_a', target: 'step_b' },
            { source: 'step_b', target: 'step_c' },
          ],
          stepData: {
            step_a: { id: 'step_a', kind: 'run', command: 'cmd-a' },
            step_b: { id: 'step_b', kind: 'run', command: 'cmd-b' },
            step_c: { id: 'step_c', kind: 'run', command: 'cmd-c' },
          },
          recipeDefaults: {}, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
          runStepId: null, runCount: 0,
        },
      },
      active: 'graph.cue', schema: {},
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
  });

  it('Test A: step-scoped run (runStepId = step_b) includes only step_b + its ancestor step_a', () => {
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'graph.cue': { ...s.docs['graph.cue'], runStepId: 'step_b', runCount: 1 } },
    }));

    render(<RunPanel {...props()} />);

    const captured = screen.getByTestId('captured-recipe').textContent ?? '';
    // Exactly 2 steps (step_a, step_b), in order, each with its own command,
    // and step_b depends on step_a (the filtered-in edge) — step_a has no
    // depends (its only outgoing edge, not incoming, survived the filter).
    // Anchored top-to-bottom: any dropped target, missing ancestor, or leaked
    // step_c would fail this match.
    expect(captured).toMatch(/^step_a:cmd-a:\|step_b:cmd-b:step_a$/);
    expect(captured).not.toContain('step_c');
    expect(captured).not.toContain('cmd-c');
  });

  it('Test B: whole-recipe run (runStepId = null) includes all 3 steps', () => {
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'graph.cue': { ...s.docs['graph.cue'], runStepId: null, runCount: 1 } },
    }));

    render(<RunPanel {...props()} />);

    const captured = screen.getByTestId('captured-recipe').textContent ?? '';
    // All 3 steps, in order, full dependency chain intact.
    expect(captured).toMatch(/^step_a:cmd-a:\|step_b:cmd-b:step_a\|step_c:cmd-c:step_b$/);
    expect(captured).toContain('step_c');
    expect(captured).toContain('cmd-c');
  });
});

// Resume-from-here ("downstream" run mode): the built recipe must contain the
// target step + its DESCENDANTS (forward BFS over edges), not its ancestors —
// the mirror image of the "Run Step" (upstream/ancestors) filtering tested
// above. Same linear graph shape (a -> b -> c) so both modes can be asserted
// against the same fixture: targeting 'b' downstream should include b + c
// (not a); targeting 'b' upstream should include a + b (not c).
describe('RunPanel downstream vs upstream mode (Resume from here vs Run Step)', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      docs: {
        'linear.cue': {
          recipeId: 'linear.cue', name: 'linear',
          nodes: [{ id: 'a' }, { id: 'b' }, { id: 'c' }],
          edges: [
            { source: 'a', target: 'b' },
            { source: 'b', target: 'c' },
          ],
          stepData: {
            a: { id: 'a', kind: 'run', command: 'cmd-a' },
            b: { id: 'b', kind: 'run', command: 'cmd-b' },
            c: { id: 'c', kind: 'run', command: 'cmd-c' },
          },
          recipeDefaults: {}, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
          runStepId: 'b', runCount: 1, runMode: 'downstream', runExtraEnv: [],
        },
      },
      active: 'linear.cue', schema: {},
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
  });

  it('downstream mode (runStepId=b) builds b + its descendant c, NOT ancestor a', () => {
    render(<RunPanel {...props()} />);

    const captured = screen.getByTestId('captured-recipe').textContent ?? '';
    // b has no depends (its only incoming edge, from a, was filtered out since
    // a is not in the descendant set); c depends on b (the filtered-in edge).
    expect(captured).toMatch(/^b:cmd-b:\|c:cmd-c:b$/);
    expect(captured).not.toContain('cmd-a');
  });

  it('upstream mode (runStepId=b) builds a + b, NOT descendant c', () => {
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'linear.cue': { ...s.docs['linear.cue'], runMode: 'upstream' } },
    }));

    render(<RunPanel {...props()} />);

    const captured = screen.getByTestId('captured-recipe').textContent ?? '';
    expect(captured).toMatch(/^a:cmd-a:\|b:cmd-b:a$/);
    expect(captured).not.toContain('cmd-c');
  });
});

// Regression for the singleton-RunPanel stale-doc bug: RunPanel is mounted
// once by the dockview shell and just follows `active` into `s.docs`, so
// switching the active recipe tab does NOT remount RunPanel itself — only
// the WizardProvider's `key` can force StepRun to re-seed from the
// newly-active doc. Two docs that happen to share (runStepId, runCount) —
// here both are runStepId=null/runCount=1, the common "just ran the whole
// recipe" shape — used to collide on the old `${stepId ?? 'all'}-${runCount}`
// key, so React reused the WizardProvider instance across the switch and
// StepRun kept rendering doc A's recipe even though `active` (and `doc`) had
// already moved to B. Asserting on the captured-recipe stub (fed by the REAL
// WizardContext, per the mock above) is what makes this test fail under the
// old key and pass under the fix — a shallow "no active run" or hosts-count
// check wouldn't have caught it.
describe('RunPanel remounts WizardProvider/StepRun on active-doc switch', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      docs: {
        'doc-a.cue': {
          recipeId: 'doc-a.cue', name: 'doc-a',
          nodes: [{ id: 'step_a' }],
          edges: [],
          stepData: { step_a: { id: 'step_a', kind: 'run', command: 'cmd-a-only' } },
          recipeDefaults: {}, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
          // Same (runStepId, runCount) as doc-b below — this is the collision
          // that the old key (`${stepId ?? 'all'}-${runCount}`, no doc
          // identity) could not distinguish.
          runStepId: null, runCount: 1,
        },
        'doc-b.cue': {
          recipeId: 'doc-b.cue', name: 'doc-b',
          nodes: [{ id: 'step_b' }],
          edges: [],
          stepData: { step_b: { id: 'step_b', kind: 'run', command: 'cmd-b-only' } },
          recipeDefaults: {}, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
          runStepId: null, runCount: 1,
        },
      },
      active: 'doc-a.cue', schema: {},
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
  });

  it('shows doc B\'s recipe (not stale doc A) after the active doc switches', () => {
    const { rerender } = render(<RunPanel {...props()} />);

    // Sanity: doc A's recipe is showing before the switch.
    expect(screen.getByTestId('captured-recipe').textContent).toContain('cmd-a-only');

    // Simulate the dockview shell's singleton RunPanel following a tab
    // switch: only `active` changes in the store, RunPanel itself is never
    // unmounted by the shell.
    useWorkspaceStore.setState({ active: 'doc-b.cue' });
    rerender(<RunPanel {...props()} />);

    const captured = screen.getByTestId('captured-recipe').textContent ?? '';
    expect(captured).toContain('cmd-b-only');
    expect(captured).not.toContain('cmd-a-only');
  });
});
