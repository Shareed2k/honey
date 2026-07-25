import { useMemo } from 'react';
import type { Node, NodeMouseHandler } from '@xyflow/react';
import { ReactFlow, Background, Controls } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import type { IDockviewPanelProps } from 'dockview';
import { useWorkspaceStore } from '../store';
import CustomStepNode from '../../CustomStepNode';

const nodeTypes = { step: CustomStepNode };

export function GraphPanel({ params }: IDockviewPanelProps<{ recipeId: string }>) {
  const recipeId = params.recipeId;
  const doc = useWorkspaceStore((s) => s.docs[recipeId]);
  const setSelectedNode = useWorkspaceStore((s) => s.setSelectedNode);
  const onNodesChange = useWorkspaceStore((s) => s.onNodesChange);
  const onEdgesChange = useWorkspaceStore((s) => s.onEdgesChange);
  const onConnect = useWorkspaceStore((s) => s.onConnect);

  // Render-only merge of doc.runStatus into each node's data — CustomStepNode
  // reads data.runStatus to color a node's border while a run is in flight
  // (running/ok/err/skipped). onNodesChange still drives doc.nodes via the
  // store; this array is only what's fed to <ReactFlow nodes=...>, so a drag/
  // delete/connect still round-trips through the store's real nodes untouched
  // by runStatus. Must run before the `!doc` early return (rules of hooks).
  const nodes = useMemo(
    () => doc?.nodes.map((n) => ({ ...n, data: { ...n.data, runStatus: doc.runStatus[n.id] } })) ?? [],
    [doc?.nodes, doc?.runStatus],
  );

  if (!doc) return <div style={{ padding: 16, color: '#8b949e' }}>No document for {recipeId}</div>;

  const onNodeClick: NodeMouseHandler = (_, node: Node) => setSelectedNode(recipeId, node.id);

  return (
    <div style={{ height: '100%', width: '100%', position: 'relative' }}>
      <ReactFlow
        nodes={nodes}
        edges={doc.edges}
        nodeTypes={nodeTypes}
        onNodeClick={onNodeClick}
        onNodesChange={(changes) => onNodesChange(recipeId, changes)}
        onEdgesChange={(changes) => onEdgesChange(recipeId, changes)}
        onConnect={(connection) => onConnect(recipeId, connection)}
        fitView
      >
        <Controls />
        <Background />
      </ReactFlow>
      {doc.rawMode && (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            background: 'rgba(13, 17, 23, 0.85)',
            color: '#8b949e',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            textAlign: 'center',
            padding: 16,
          }}
        >
          Raw mode — switch to Visual to edit the graph.
        </div>
      )}
    </div>
  );
}
