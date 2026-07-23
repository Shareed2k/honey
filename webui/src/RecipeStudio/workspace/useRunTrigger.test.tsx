import { cleanup, render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { useWorkspaceStore } from './store';
import { useRunTrigger } from './useRunTrigger';

// The real ParameterPromptModal drives an antd <Form>/<Modal> against
// whatever `prompts` shape the recipe defines — that's already covered by
// ParameterPromptModal.tsx's own behavior. What useRunTrigger owns is the
// GATE around it (open vs. run-immediately) and the vals -> extraEnv
// conversion feeding startRun, so this stub exposes exactly those two
// surfaces (onSubmit/onCancel) plus the prompts it was given, without
// re-testing the modal's form internals.
vi.mock('../ParameterPromptModal', () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ParameterPromptModal: (props: any) =>
    props.open ? (
      <div>
        <div data-testid="prompt-modal">prompts:{Object.keys(props.prompts).join(',')}</div>
        <button onClick={() => props.onSubmit({ region: 'us-east-1' })}>submit-prompts</button>
        <button onClick={() => props.onCancel()}>cancel-prompts</button>
      </div>
    ) : null,
}));

function Harness({ recipeId }: { recipeId: string | null }) {
  const { run, promptModal } = useRunTrigger(recipeId);
  return (
    <div>
      <button onClick={() => run('node1', 'upstream')}>trigger-upstream</button>
      <button onClick={() => run('node1', 'downstream')}>trigger-downstream</button>
      <button onClick={() => run(null)}>trigger-whole-recipe</button>
      {promptModal}
    </div>
  );
}

afterEach(cleanup);

function seedDoc(recipeDefaults: unknown = {}) {
  useWorkspaceStore.setState({
    docs: {
      'a.cue': {
        recipeId: 'a.cue', name: 'a', nodes: [], edges: [], stepData: {},
        recipeDefaults, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
        validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
        runStepId: null, runCount: 0, runMode: 'upstream', runExtraEnv: [],
      },
    },
    active: 'a.cue', schema: {},
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  } as any);
}

describe('useRunTrigger', () => {
  beforeEach(() => seedDoc());

  it('with no prompts, run() calls startRun directly with extraEnv []', () => {
    const startRunSpy = vi.spyOn(useWorkspaceStore.getState(), 'startRun').mockImplementation(() => {});

    render(<Harness recipeId="a.cue" />);
    fireEvent.click(screen.getByRole('button', { name: 'trigger-upstream' }));

    expect(startRunSpy).toHaveBeenCalledWith('a.cue', 'node1', 'upstream', []);
    expect(screen.queryByTestId('prompt-modal')).toBeNull();

    startRunSpy.mockRestore();
  });

  it('with no prompts, a downstream run() also calls startRun directly with mode \'downstream\'', () => {
    const startRunSpy = vi.spyOn(useWorkspaceStore.getState(), 'startRun').mockImplementation(() => {});

    render(<Harness recipeId="a.cue" />);
    fireEvent.click(screen.getByRole('button', { name: 'trigger-downstream' }));

    expect(startRunSpy).toHaveBeenCalledWith('a.cue', 'node1', 'downstream', []);

    startRunSpy.mockRestore();
  });

  it('with a non-empty recipeDefaults.prompts, run() does NOT call startRun and opens the prompt modal instead', () => {
    seedDoc({ prompts: { region: { description: 'AWS region' } } });
    const startRunSpy = vi.spyOn(useWorkspaceStore.getState(), 'startRun').mockImplementation(() => {});

    render(<Harness recipeId="a.cue" />);
    fireEvent.click(screen.getByRole('button', { name: 'trigger-upstream' }));

    expect(startRunSpy).not.toHaveBeenCalled();
    expect(screen.getByTestId('prompt-modal').textContent).toContain('region');

    startRunSpy.mockRestore();
  });

  it('submitting the prompt modal calls startRun with the pending (stepId, mode) and extraEnv derived from the submitted values', () => {
    seedDoc({ prompts: { region: { description: 'AWS region' } } });
    const startRunSpy = vi.spyOn(useWorkspaceStore.getState(), 'startRun').mockImplementation(() => {});

    render(<Harness recipeId="a.cue" />);
    fireEvent.click(screen.getByRole('button', { name: 'trigger-downstream' }));
    fireEvent.click(screen.getByRole('button', { name: 'submit-prompts' }));

    expect(startRunSpy).toHaveBeenCalledWith('a.cue', 'node1', 'downstream', [{ key: 'region', value: 'us-east-1' }]);

    startRunSpy.mockRestore();
  });

  it('a whole-recipe trigger (stepId null) that goes through the prompt gate resumes with stepId null after submit', () => {
    seedDoc({ prompts: { region: { description: 'AWS region' } } });
    const startRunSpy = vi.spyOn(useWorkspaceStore.getState(), 'startRun').mockImplementation(() => {});

    render(<Harness recipeId="a.cue" />);
    fireEvent.click(screen.getByRole('button', { name: 'trigger-whole-recipe' }));
    fireEvent.click(screen.getByRole('button', { name: 'submit-prompts' }));

    expect(startRunSpy).toHaveBeenCalledWith('a.cue', null, 'upstream', [{ key: 'region', value: 'us-east-1' }]);

    startRunSpy.mockRestore();
  });

  it('cancelling the prompt modal does not call startRun', () => {
    seedDoc({ prompts: { region: { description: 'AWS region' } } });
    const startRunSpy = vi.spyOn(useWorkspaceStore.getState(), 'startRun').mockImplementation(() => {});

    render(<Harness recipeId="a.cue" />);
    fireEvent.click(screen.getByRole('button', { name: 'trigger-upstream' }));
    fireEvent.click(screen.getByRole('button', { name: 'cancel-prompts' }));

    expect(startRunSpy).not.toHaveBeenCalled();
    expect(screen.queryByTestId('prompt-modal')).toBeNull();

    startRunSpy.mockRestore();
  });

  it('run() on a null recipeId is a no-op (no throw, no startRun call)', () => {
    const startRunSpy = vi.spyOn(useWorkspaceStore.getState(), 'startRun').mockImplementation(() => {});

    render(<Harness recipeId={null} />);
    expect(() => fireEvent.click(screen.getByRole('button', { name: 'trigger-upstream' }))).not.toThrow();

    expect(startRunSpy).not.toHaveBeenCalled();

    startRunSpy.mockRestore();
  });
});
