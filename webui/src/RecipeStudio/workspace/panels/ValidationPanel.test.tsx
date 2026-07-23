import { cleanup, render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { message } from 'antd';
import { useWorkspaceStore } from '../store';
import { ValidationPanel } from './ValidationPanel';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function props(): any {
  return { params: {}, api: {}, containerApi: {} };
}

function baseDoc(overrides: Record<string, unknown> = {}) {
  return {
    recipeId: 'deploy.cue', name: 'deploy',
    nodes: [], edges: [], stepData: {},
    recipeDefaults: {}, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
    validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
    runStepId: null, runCount: 0,
    ...overrides,
  };
}

afterEach(cleanup);

describe('ValidationPanel', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      docs: { 'deploy.cue': baseDoc() },
      active: 'deploy.cue',
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
  });

  it('shows a muted hint when there is no active document', () => {
    useWorkspaceStore.setState({ active: null });

    render(<ValidationPanel {...props()} />);

    expect(screen.getByText(/no active document/i)).toBeTruthy();
  });

  it('shows a "Validating…" alert while validation.state is "validating"', () => {
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'deploy.cue': { ...s.docs['deploy.cue'], validation: { state: 'validating', issues: [] } } },
    }));

    render(<ValidationPanel {...props()} />);

    expect(screen.getByText(/validating/i)).toBeTruthy();
  });

  it('shows a success alert when validation.state is "valid"', () => {
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'deploy.cue': { ...s.docs['deploy.cue'], validation: { state: 'valid', issues: [] } } },
    }));

    render(<ValidationPanel {...props()} />);

    expect(screen.getByText(/recipe is valid/i)).toBeTruthy();
  });

  it('renders each issue from doc.validation.issues (path + message)', () => {
    useWorkspaceStore.setState((s) => ({
      docs: {
        ...s.docs,
        'deploy.cue': {
          ...s.docs['deploy.cue'],
          validation: {
            state: 'invalid',
            issues: [
              { path: 'steps.step_a', message: 'host is required' },
              { message: 'missing command' },
            ],
          },
        },
      },
    }));

    render(<ValidationPanel {...props()} />);

    expect(screen.getByText('2 issues')).toBeTruthy();
    expect(screen.getByText(/steps\.step_a:\s*host is required/)).toBeTruthy();
    expect(screen.getByText('missing command')).toBeTruthy();
  });

  it('renders a risk Alert (level/score/findings) when risk.score > 0', () => {
    useWorkspaceStore.setState((s) => ({
      docs: {
        ...s.docs,
        'deploy.cue': {
          ...s.docs['deploy.cue'],
          validation: {
            state: 'valid',
            issues: [],
            risk: { score: 8, level: 'High', findings: ['runs as root', 'no session recording'] },
          },
        },
      },
    }));

    render(<ValidationPanel {...props()} />);

    expect(screen.getByText(/Risk Level: High \(Score: 8\)/)).toBeTruthy();
    expect(screen.getByText('runs as root')).toBeTruthy();
    expect(screen.getByText('no session recording')).toBeTruthy();
  });

  it('does not render a risk Alert when risk.score is 0', () => {
    useWorkspaceStore.setState((s) => ({
      docs: {
        ...s.docs,
        'deploy.cue': {
          ...s.docs['deploy.cue'],
          validation: { state: 'valid', issues: [], risk: { score: 0, level: 'Low', findings: [] } },
        },
      },
    }));

    render(<ValidationPanel {...props()} />);

    expect(screen.queryByText(/Risk Level/)).toBeNull();
  });

  it('hides the "Fix with AI" button when there are no issues', () => {
    render(<ValidationPanel {...props()} />);

    expect(screen.queryByRole('button', { name: /fix with ai/i })).toBeNull();
  });

  it('"Fix with AI" calls store.fixWithAI for the active doc and toasts success', async () => {
    useWorkspaceStore.setState((s) => ({
      docs: {
        ...s.docs,
        'deploy.cue': { ...s.docs['deploy.cue'], validation: { state: 'invalid', issues: [{ message: 'bad' }] } },
      },
    }));
    const fixSpy = vi.spyOn(useWorkspaceStore.getState(), 'fixWithAI').mockResolvedValue(undefined);
    const successSpy = vi.spyOn(message, 'success').mockImplementation(() => ({}) as never);

    render(<ValidationPanel {...props()} />);
    fireEvent.click(screen.getByRole('button', { name: /fix with ai/i }));

    await waitFor(() => expect(fixSpy).toHaveBeenCalledWith('deploy.cue'));
    await waitFor(() => expect(successSpy).toHaveBeenCalled());

    fixSpy.mockRestore();
    successSpy.mockRestore();
  });

  it('a failing "Fix with AI" call shows an error toast (and does not throw)', async () => {
    useWorkspaceStore.setState((s) => ({
      docs: {
        ...s.docs,
        'deploy.cue': { ...s.docs['deploy.cue'], validation: { state: 'invalid', issues: [{ message: 'bad' }] } },
      },
    }));
    const fixSpy = vi.spyOn(useWorkspaceStore.getState(), 'fixWithAI').mockRejectedValue(new Error('boom'));
    const errorSpy = vi.spyOn(message, 'error').mockImplementation(() => ({}) as never);

    render(<ValidationPanel {...props()} />);
    fireEvent.click(screen.getByRole('button', { name: /fix with ai/i }));

    await waitFor(() => expect(errorSpy).toHaveBeenCalled());
    expect(errorSpy.mock.calls[0]?.[0]).toContain('boom');

    fixSpy.mockRestore();
    errorSpy.mockRestore();
  });
});
