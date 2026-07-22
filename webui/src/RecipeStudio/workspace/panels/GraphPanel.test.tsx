import { cleanup, render, screen } from '@testing-library/react';
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { useWorkspaceStore } from '../store';
import { GraphPanel } from './GraphPanel';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function props(recipeId: string): any {
  return { params: { recipeId }, api: {}, containerApi: {} };
}

afterEach(cleanup);

describe('GraphPanel', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      docs: {
        'deploy.cue': {
          recipeId: 'deploy.cue', name: 'deploy', nodes: [], edges: [], stepData: {},
          recipeDefaults: {}, selectedNodeId: null, rawMode: false, rawContent: '', originalCue: '',
          validation: { state: 'idle', issues: [] }, runStatus: {}, dirty: false,
        },
      },
      active: 'deploy.cue',
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
  });

  it('renders the ReactFlow canvas for its recipe', () => {
    render(<GraphPanel {...props('deploy.cue')} />);
    expect(document.querySelector('.react-flow')).toBeTruthy();
  });

  it('shows a not-found notice for an unknown recipe id', () => {
    render(<GraphPanel {...props('missing.cue')} />);
    expect(screen.getByText(/no document/i)).toBeTruthy();
  });

  it('shows a raw-mode overlay when the doc is in raw mode', () => {
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'deploy.cue': { ...s.docs['deploy.cue'], rawMode: true } },
    }));
    render(<GraphPanel {...props('deploy.cue')} />);
    expect(screen.getByText(/raw mode.*switch to visual/i)).toBeTruthy();
  });
});
