import { cleanup, render, screen } from '@testing-library/react';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { useWorkspaceStore } from '../store';

// Captures the exact props ReactFlow is rendered with so the wiring test below
// can invoke the captured onConnect/onNodesChange/onEdgesChange directly and
// assert the store actually changed — proving the panel->store wiring
// end-to-end rather than just checking "the prop is a function".
// eslint-disable-next-line @typescript-eslint/no-explicit-any
let capturedProps: any = null;
vi.mock('@xyflow/react', async (importOriginal) => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const actual = await importOriginal<any>();
  return {
    ...actual,
    ReactFlow: (props: Record<string, unknown>) => {
      capturedProps = props;
      return <div className="react-flow" />;
    },
  };
});

import { GraphPanel } from './GraphPanel';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function props(recipeId: string): any {
  return { params: { recipeId }, api: {}, containerApi: {} };
}

afterEach(() => {
  cleanup();
  capturedProps = null;
});

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

  // This is the regression test for the "can't connect/move/delete steps" bug:
  // GraphPanel used to only pass onNodeClick to <ReactFlow>, so ReactFlow had
  // no way to apply node/edge changes or new connections back to the store.
  // Rather than just asserting "the prop is a function", this invokes the
  // captured callbacks and asserts the store's doc actually changed —
  // proving the panel->store wiring end-to-end.
  it('wires onConnect/onNodesChange/onEdgesChange from ReactFlow through to the store', () => {
    useWorkspaceStore.setState((s) => ({
      docs: {
        ...s.docs,
        'deploy.cue': {
          ...s.docs['deploy.cue'],
          nodes: [
            { id: 'a', type: 'step', position: { x: 0, y: 0 }, data: {} },
            { id: 'b', type: 'step', position: { x: 100, y: 0 }, data: {} },
          ],
        },
      },
    }));

    render(<GraphPanel {...props('deploy.cue')} />);
    expect(typeof capturedProps.onConnect).toBe('function');
    expect(typeof capturedProps.onNodesChange).toBe('function');
    expect(typeof capturedProps.onEdgesChange).toBe('function');

    // onConnect: dragging a new edge between two nodes must reach the store.
    expect(useWorkspaceStore.getState().docs['deploy.cue'].edges).toHaveLength(0);
    capturedProps.onConnect({ source: 'a', target: 'b', sourceHandle: null, targetHandle: null });
    let doc = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(doc.edges).toHaveLength(1);
    expect(doc.edges[0].source).toBe('a');
    expect(doc.edges[0].target).toBe('b');
    expect(doc.dirty).toBe(true);

    // onNodesChange: removing a node must reach the store.
    capturedProps.onNodesChange([{ type: 'remove', id: 'b' }]);
    doc = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(doc.nodes).toHaveLength(1);
    expect(doc.nodes.find((n) => n.id === 'b')).toBeUndefined();

    // onEdgesChange: removing an edge must reach the store.
    const edgeId = doc.edges[0].id;
    capturedProps.onEdgesChange([{ type: 'remove', id: edgeId }]);
    doc = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(doc.edges).toHaveLength(0);
  });
});
